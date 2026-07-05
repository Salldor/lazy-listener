package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gen2brain/malgo"
)

func main() {
	modelPath := flag.String("model", "./models/ggml-small.bin", "path to whisper.cpp ggml model file")
	lang := flag.String("lang", "ru", "transcription language: ru, en, de, fr, …")
	flag.Parse()

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		fatal("init context", err)
	}
	defer func() { _ = ctx.Uninit() }()

	captureID, err := selectDevice(ctx, malgo.Capture, false)
	if err != nil {
		fatal("select capture", err)
	}

	playbackID, err := selectDevice(ctx, malgo.Playback, true)
	if err != nil {
		fatal("select playback", err)
	}

	out, err := newOutput()
	if err != nil {
		fatal("output", err)
	}
	defer out.Close()

	wav, err := newWAVWriter()
	if err != nil {
		fatal("wav writer", err)
	}
	defer wav.Close()

	tr, err := newTranscriber(*modelPath, *lang, func(text string) {
		out.Write(text)
	})
	if err != nil {
		fatal("transcriber", err)
	}
	defer tr.Close()

	sessionStart := time.Now()
	vad, err := newVAD(sessionStart, tr.Feed)
	if err != nil {
		fatal("vad", err)
	}

	devType := malgo.Capture
	if playbackID != nil {
		devType = malgo.Duplex
	}

	cfg := malgo.DefaultDeviceConfig(devType)
	cfg.Capture.DeviceID = captureID
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.SampleRate = captureRate
	cfg.Alsa.NoMMap = 1

	if playbackID != nil {
		cfg.Playback.DeviceID = playbackID
		cfg.Playback.Format = malgo.FormatS16
		cfg.Playback.Channels = 1
	}

	callbacks := malgo.DeviceCallbacks{
		Data: func(outputSamples, inputSamples []byte, _ uint32) {
			wav.Write(inputSamples)
			vad.Feed(inputSamples)
			// Monitor mode: echo input back to speakers when playback is active.
			if playbackID != nil && len(outputSamples) > 0 {
				copy(outputSamples, inputSamples)
			}
		},
	}

	device, err := malgo.InitDevice(ctx.Context, cfg, callbacks)
	if err != nil {
		fatal("init device", err)
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		fatal("start device", err)
	}

	fmt.Println("\nRecording... Press Ctrl+C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("\nStopping...")
}

func fatal(label string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
	os.Exit(1)
}
