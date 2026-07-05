package main

import (
	"encoding/binary"
	"time"

	webrtcvad "github.com/maxhawkins/go-webrtcvad"
)

const (
	captureRate      = 16000
	frameSamples     = 480                  // 30 ms @ 16 kHz
	frameBytes       = frameSamples * 2     // S16LE: 2 bytes/sample
	prerollFrames    = 10                   // 300 ms look-back before speech onset
	maxSilenceFrames = 23                   // ~700 ms of silence ends a segment
	maxSpeechBytes   = captureRate * 2 * 30 // 30 s hard cap
)

// SpeechSegment carries a transcribable audio chunk with its wall-clock timing.
type SpeechSegment struct {
	Samples []float32
	Start   time.Duration // offset from session start
	End     time.Duration
}

type VADProcessor struct {
	detector           *webrtcvad.VAD
	onSpeech           func(SpeechSegment)
	minActiveFrames    int
	sessionStart       time.Time
	segStart           time.Time
	frameAccum         []byte
	preroll            [prerollFrames][]byte
	prerollHead        int
	speechBuf          []byte
	inSpeech           bool
	silenceCount       int
	activeSpeechFrames int
}

func newVAD(sessionStart time.Time, minActiveFrames int, onSpeech func(SpeechSegment)) (*VADProcessor, error) {
	v, err := webrtcvad.New()
	if err != nil {
		return nil, err
	}
	if err := v.SetMode(3); err != nil {
		return nil, err
	}
	return &VADProcessor{
		detector:        v,
		onSpeech:        onSpeech,
		minActiveFrames: minActiveFrames,
		sessionStart:    sessionStart,
	}, nil
}

func (p *VADProcessor) Feed(data []byte) {
	p.frameAccum = append(p.frameAccum, data...)
	for len(p.frameAccum) >= frameBytes {
		frame := make([]byte, frameBytes)
		copy(frame, p.frameAccum[:frameBytes])
		p.frameAccum = p.frameAccum[frameBytes:]
		p.processFrame(frame)
	}
}

func (p *VADProcessor) processFrame(frame []byte) {
	// Always rotate the pre-roll ring buffer.
	p.preroll[p.prerollHead%prerollFrames] = frame
	p.prerollHead++

	active, err := p.detector.Process(captureRate, frame)
	if err != nil {
		return
	}

	if active {
		if !p.inSpeech {
			p.inSpeech = true
			p.silenceCount = 0
			p.segStart = time.Now()
			// Prepend buffered pre-roll so we don't miss word beginnings.
			for i := 0; i < prerollFrames; i++ {
				idx := (p.prerollHead - prerollFrames + i + prerollFrames) % prerollFrames
				if p.preroll[idx] != nil {
					p.speechBuf = append(p.speechBuf, p.preroll[idx]...)
				}
			}
		}
		p.silenceCount = 0
		p.activeSpeechFrames++
		p.speechBuf = append(p.speechBuf, frame...)

		if len(p.speechBuf) >= maxSpeechBytes {
			p.flush()
		}
		return
	}

	if p.inSpeech {
		p.speechBuf = append(p.speechBuf, frame...)
		p.silenceCount++
		if p.silenceCount >= maxSilenceFrames {
			p.flush()
		}
	}
}

func (p *VADProcessor) flush() {
	if len(p.speechBuf) == 0 {
		return
	}
	active := p.activeSpeechFrames
	seg := SpeechSegment{
		Samples: s16ToFloat32(p.speechBuf),
		Start:   p.segStart.Sub(p.sessionStart),
		End:     time.Now().Sub(p.sessionStart),
	}
	p.speechBuf = p.speechBuf[:0]
	p.inSpeech = false
	p.silenceCount = 0
	p.activeSpeechFrames = 0

	if active < p.minActiveFrames {
		return // too little real speech — likely noise, skip
	}
	p.onSpeech(seg)
}

func s16ToFloat32(data []byte) []float32 {
	out := make([]float32, len(data)/2)
	for i := range out {
		s := int16(binary.LittleEndian.Uint16(data[i*2:]))
		out[i] = float32(s) / 32768.0
	}
	return out
}
