package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

type Output struct {
	w    io.Writer
	file *os.File
	path string
}

func newOutput() (*Output, error) {
	if err := os.MkdirAll("transcripts", 0o755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("transcripts/transcript_%s.txt", time.Now().Format("2006_01_02_15_04_05"))
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Transcript → %s\n", name)
	return &Output{w: io.MultiWriter(os.Stdout, f), file: f, path: name}, nil
}

func (o *Output) Path() string { return o.path }

func (o *Output) Write(text string) {
	fmt.Fprintln(o.w, text)
}

func (o *Output) Close() {
	_ = o.file.Close()
}
