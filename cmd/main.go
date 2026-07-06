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
	if len(os.Args) > 1 && os.Args[1] == "recap" {
		runRecap(os.Args[2:])
		return
	}

	modelPath := flag.String("model", "./models/ggml-small.bin", "path to whisper.cpp ggml model file")
	lang := flag.String("lang", "ru", "transcription language: ru, en, de, fr, …")
	minRMS := flag.Float64("min-rms", 0.005, "minimum RMS energy to send segment to whisper (0=off)")
	minSpeechFrames := flag.Int("min-speech-ms", 90, "minimum VAD-confirmed speech duration in ms before transcribing")
	diarize := flag.Bool("diarize", false, "enable speaker diarization via speaker_tracker.py (requires: pip install resemblyzer flask)")
	silenceMS := flag.Int("silence-ms", 700, "ms of silence that ends a speech segment (diarize default: 350)")
	maxSegmentMS := flag.Int("max-segment-ms", 30000, "hard cap on segment duration in ms (diarize default: 6000)")
	meetingType := flag.String("meeting-type", "general", "meeting type for recap: general, grooming, planning")
	recapModel := flag.String("recap-model", "gemma4:latest", "Ollama model for recap generation")
	ollamaURL := flag.String("ollama", "http://localhost:11434", "Ollama server base URL")
	flag.Parse()

	if *diarize {
		if !isFlagSet("silence-ms") {
			*silenceMS = 350
		}
		if !isFlagSet("max-segment-ms") {
			*maxSegmentMS = 6000
		}
	}

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

	wav, err := newWAVWriter()
	if err != nil {
		fatal("wav writer", err)
	}

	tr, err := newTranscriber(*modelPath, *lang, *minRMS, *diarize, func(text string) {
		out.Write(text)
	})
	if err != nil {
		fatal("transcriber", err)
	}

	sessionStart := time.Now()
	minFrames := *minSpeechFrames / 30 // convert ms → frame count (1 frame = 30 ms)
	if minFrames < 1 {
		minFrames = 1
	}
	maxSilenceFrames := *silenceMS / 30
	if maxSilenceFrames < 1 {
		maxSilenceFrames = 1
	}
	maxSegmentBytes := *maxSegmentMS * captureRate * 2 / 1000
	vad, err := newVAD(sessionStart, minFrames, maxSilenceFrames, maxSegmentBytes, tr.Feed)
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

	device.Stop()
	tr.Close()
	wav.Close()
	out.Close()

	recapFromFile(out.Path(), *meetingType, *recapModel, *ollamaURL)
}

func fatal(label string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
	os.Exit(1)
}

func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
