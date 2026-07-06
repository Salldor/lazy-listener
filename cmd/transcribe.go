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
	"path/filepath"
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

	speakerHost = "127.0.0.1"
	speakerPort = "18766"
	speakerAddr = speakerHost + ":" + speakerPort
)

type Transcriber struct {
	server        *exec.Cmd
	speakerServer *exec.Cmd
	client        *http.Client
	onText        func(string)
	minRMS        float64
	diarize       bool
	queue         chan SpeechSegment
	done          chan struct{}
}

func newTranscriber(modelPath, lang string, minRMS float64, diarize bool, onText func(string)) (*Transcriber, error) {
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
		server:  cmd,
		client:  &http.Client{Timeout: 120 * time.Second},
		onText:  onText,
		minRMS:  minRMS,
		diarize: diarize,
		queue:   make(chan SpeechSegment, 4),
		done:    make(chan struct{}),
	}

	if diarize {
		if err := t.startSpeakerTracker(); err != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("speaker-tracker: %w", err)
		}
	}

	go t.run()
	return t, nil
}

func (t *Transcriber) startSpeakerTracker() error {
	exe, err := os.Executable()
	if err != nil {
		exe = "."
	}
	script := filepath.Join(filepath.Dir(exe), "speaker_tracker.py")

	logFile, err := os.Create("speaker-tracker.log")
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}

	cmd := exec.Command("python3", script, "--port", speakerPort)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start: %w", err)
	}

	fmt.Print("Starting speaker tracker... ")
	if err := waitForPort(speakerAddr, 30*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		return fmt.Errorf("timeout: %w", err)
	}
	fmt.Println("ready.")

	t.speakerServer = cmd
	return nil
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
		wav := samplesToWAV(seg.Samples)
		ts := fmt.Sprintf("[%s → %s]", formatTS(seg.Start), formatTS(seg.End))

		if !t.diarize {
			text, err := t.transcribeWAV(wav)
			if err == nil && text != "" && !isHallucination(text, seg.End-seg.Start) {
				t.onText(ts + " " + text)
			}
			continue
		}

		type strResult struct {
			val string
			err error
		}
		textCh := make(chan strResult, 1)
		spkCh := make(chan strResult, 1)

		go func() {
			v, err := t.transcribeWAV(wav)
			textCh <- strResult{v, err}
		}()
		go func() {
			v, err := t.identify(wav)
			spkCh <- strResult{v, err}
		}()

		tr := <-textCh
		sr := <-spkCh

		if tr.err == nil && tr.val != "" && !isHallucination(tr.val, seg.End-seg.Start) {
			speaker := "Speaker ?"
			if sr.err == nil && sr.val != "" {
				speaker = sr.val
			}
			t.onText(ts + " " + speaker + ": " + tr.val)
		}
	}
}

func (t *Transcriber) transcribeWAV(wav []byte) (string, error) {
	body, ct, err := wavMultipart(wav, "audio.wav")
	if err != nil {
		return "", err
	}
	resp, err := t.client.Post("http://"+whisperAddr+"/inference", ct, body)
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

func (t *Transcriber) identify(wav []byte) (string, error) {
	body, ct, err := wavMultipart(wav, "audio.wav")
	if err != nil {
		return "", err
	}
	resp, err := t.client.Post("http://"+speakerAddr+"/identify", ct, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		Speaker string `json:"speaker"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Speaker, nil
}

// whisperHallucinations contains substrings whisper produces on silence/noise.
// These are training-data artifacts (subtitled YouTube content) with no speech content.
var whisperHallucinations = []string{
	"продолжение следует",
	"субтитры сделал",
	"субтитры добавил",
	"субтитры создал",
	"субтитры:",
	"редактор субтитров",
	"переведено для",
	"amara.org",
	"subscribetoмоему",
	"подписывайтесь на канал",
	"thanks for watching",
	"thank you for watching",
	"please subscribe",
	"like and subscribe",
	"www.",
	".com",
}

// isHallucination returns true if text is a known whisper artifact or suspiciously
// sparse for its segment duration (a single short word over several seconds of audio).
func isHallucination(text string, dur time.Duration) bool {
	lower := strings.ToLower(strings.TrimSpace(text))

	for _, h := range whisperHallucinations {
		if strings.Contains(lower, h) {
			return true
		}
	}

	// Single very short word in a segment longer than 2 seconds is almost always noise.
	words := strings.Fields(lower)
	if dur >= 2*time.Second && len(words) == 1 && len([]rune(words[0])) <= 6 {
		return true
	}

	return false
}

func wavMultipart(wav []byte, filename string) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(fw, bytes.NewReader(wav)); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return body, mw.FormDataContentType(), nil
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
	if t.speakerServer != nil && t.speakerServer.Process != nil {
		_ = t.speakerServer.Process.Kill()
	}
}
