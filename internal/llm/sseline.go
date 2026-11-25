package llm

import (
	"bufio"
	"errors"
)

// maxStreamLine bounds one line of a server-sent stream. A model reply line is a small JSON
// chunk; a server that never sends a newline must not be able to grow memory without limit.
const maxStreamLine = 4 << 20

var errStreamLine = errors.New("a line of the model stream is too long")

// readStreamLine reads up to the next newline, refusing a line longer than maxStreamLine.
func readStreamLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > maxStreamLine {
			return nil, errStreamLine
		}
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}
