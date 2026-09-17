package main

import "bytes"

// lineWriter is an io.Writer that invokes a callback for each complete line.
// Lines are delimited by '\n'. Carriage returns are stripped.
// A final write without a trailing newline does not produce a callback.
type lineWriter struct {
	buf      []byte
	callback func(string)
}

func newLineWriter(callback func(string)) *lineWriter {
	return &lineWriter{callback: callback}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			w.buf = append(w.buf, p...)
			break
		}
		// Emit the line including anything buffered from a previous write.
		line := append(w.buf, p[:idx]...)
		line = bytes.TrimSuffix(line, []byte{'\r'})
		w.buf = w.buf[:0]
		w.callback(string(line))
		p = p[idx+1:]
	}
	return n, nil
}
