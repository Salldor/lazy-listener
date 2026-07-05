package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"time"
)

type WAVWriter struct {
	file     *os.File
	dataSize uint32
}

func newWAVWriter() (*WAVWriter, error) {
	if err := os.MkdirAll("recordings", 0o755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("recordings/rec_%s.wav", time.Now().Format("2006_01_02_15_04_05"))
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	// Reserve 44 bytes for the WAV header; filled in on Close.
	if _, err := f.Write(make([]byte, 44)); err != nil {
		_ = f.Close()
		return nil, err
	}
	fmt.Printf("Recording  → %s\n", name)
	return &WAVWriter{file: f}, nil
}

func (w *WAVWriter) Write(pcm []byte) {
	_, _ = w.file.Write(pcm)
	w.dataSize += uint32(len(pcm))
}

func (w *WAVWriter) Close() error {
	if _, err := w.file.Seek(0, 0); err != nil {
		return err
	}
	const (
		sampleRate uint32 = 16000
		channels   uint16 = 1
		bitsPerSmp uint16 = 16
	)
	byteRate := sampleRate * uint32(channels) * uint32(bitsPerSmp) / 8
	blockAlign := channels * bitsPerSmp / 8

	buf := make([]byte, 44)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], 36+w.dataSize)
	copy(buf[8:], "WAVE")
	copy(buf[12:], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:], channels)
	binary.LittleEndian.PutUint32(buf[24:], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:], byteRate)
	binary.LittleEndian.PutUint16(buf[32:], blockAlign)
	binary.LittleEndian.PutUint16(buf[34:], bitsPerSmp)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], w.dataSize)

	if _, err := w.file.Write(buf); err != nil {
		return err
	}
	return w.file.Close()
}
