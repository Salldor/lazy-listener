package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func rms(samples []float32) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func formatTS(d time.Duration) string {
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

const (
	whisperHost = "127.0.0.1"
	whisperPort = "18765"
	whisperAddr = whisperHost + ":" + whisperPort
)

type Transcriber struct {
	server *exec.Cmd
	client *http.Client
	onText func(string)
	minRMS float64
	queue  chan SpeechSegment
	done   chan struct{}
}

func newTranscriber(modelPath, lang string, minRMS float64, onText func(string)) (*Transcriber, error) {
	logFile, err := os.Create("whisper-server.log")
	if err != nil {
		return nil, fmt.Errorf("create log: %w", err)
	}

	cmd := exec.Command("whisper-server",
		"--model", modelPath,
		"--host", whisperHost,
		"--port", whisperPort,
		"--language", lang,
		"--threads", "4",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start whisper-server: %w", err)
	}

	fmt.Print("Loading model... ")
	if err := waitForPort(whisperAddr, 60*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		return nil, fmt.Errorf("whisper-server: %w", err)
	}
	fmt.Println("ready.")

	t := &Transcriber{
		server: cmd,
		client: &http.Client{Timeout: 120 * time.Second},
		onText: onText,
		minRMS: minRMS,
		queue:  make(chan SpeechSegment, 4),
		done:   make(chan struct{}),
	}
	go t.run()
	return t, nil
}

func waitForPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

// Feed enqueues a speech segment for transcription. Drops silently under backpressure.
func (t *Transcriber) Feed(seg SpeechSegment) {
	select {
	case t.queue <- seg:
	default:
	}
}

func (t *Transcriber) run() {
	defer close(t.done)
	for seg := range t.queue {
		if rms(seg.Samples) < t.minRMS {
			continue // too quiet — noise, skip before hitting whisper
		}
		text, err := t.transcribe(seg.Samples)
		if err == nil && text != "" {
			ts := fmt.Sprintf("[%s → %s]", formatTS(seg.Start), formatTS(seg.End))
			t.onText(ts + " " + text)
		}
	}
}

func (t *Transcriber) transcribe(samples []float32) (string, error) {
	wav := samplesToWAV(samples)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, bytes.NewReader(wav)); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	resp, err := t.client.Post(
		"http://"+whisperAddr+"/inference",
		mw.FormDataContentType(),
		body,
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

// samplesToWAV encodes float32 PCM samples (16 kHz mono) as a WAV byte slice.
func samplesToWAV(samples []float32) []byte {
	pcm := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(s*32767)))
	}
	const (
		sr   uint32 = 16000
		ch   uint16 = 1
		bits uint16 = 16
	)
	ds := uint32(len(pcm))
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], 36+ds)
	copy(hdr[8:], "WAVE")
	copy(hdr[12:], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1) // PCM
	binary.LittleEndian.PutUint16(hdr[22:], ch)
	binary.LittleEndian.PutUint32(hdr[24:], sr)
	binary.LittleEndian.PutUint32(hdr[28:], sr*uint32(ch)*uint32(bits)/8)
	binary.LittleEndian.PutUint16(hdr[32:], ch*bits/8)
	binary.LittleEndian.PutUint16(hdr[34:], bits)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], ds)
	return append(hdr, pcm...)
}

func (t *Transcriber) Close() {
	close(t.queue)
	<-t.done
	if t.server != nil && t.server.Process != nil {
		_ = t.server.Process.Kill()
	}
}
