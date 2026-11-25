package llm

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadStreamLine(t *testing.T) {
	long := strings.Repeat("a", 3*4096) // longer than the reader's buffer, shorter than the cap
	r := bufio.NewReaderSize(strings.NewReader(long+"\nnext\npartial"), 4096)
	if line, err := readStreamLine(r); err != nil || string(line) != long+"\n" {
		t.Fatalf("long line: %d bytes, %v", len(line), err)
	}
	if line, err := readStreamLine(r); err != nil || string(line) != "next\n" {
		t.Fatalf("short line: %q, %v", line, err)
	}
	if _, err := readStreamLine(r); !errors.Is(err, io.EOF) {
		t.Fatalf("tail: %v", err)
	}
}

func TestReadStreamLineRefusesAnEndlessLine(t *testing.T) {
	r := bufio.NewReaderSize(bytes.NewReader(bytes.Repeat([]byte("x"), maxStreamLine+8192)), 4096)
	if _, err := readStreamLine(r); !errors.Is(err, errStreamLine) {
		t.Fatalf("got %v", err)
	}
}
