package core

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
)

type Output struct {
	mu sync.Mutex
	f  *os.File
}

func NewOutput(folder, filename string) *Output {
	outFile := filepath.Join(folder, filename)
	f, err := os.OpenFile(outFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, os.ModePerm)
	if err != nil {
		Logger.Errorf("Failed to open file to write Output: %s", err)
		os.Exit(1)
	}
	return &Output{
		f: f,
	}
}

func (o *Output) WriteToFile(msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, _ = o.f.WriteString(msg + "\n")
}

func (o *Output) Write(msg []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	b := bytes.NewBuffer(msg)
	b.Write([]byte("\n"))
	return o.f.Write(b.Bytes())
}

func (o *Output) Close() {
	o.f.Close()
}
