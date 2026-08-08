package logging

import (
	"io"
	"log"
	"os"
)

type Logger struct {
	MainDebug  bool
	FridaDebug bool
	std        *log.Logger
	err        *log.Logger
}

func New(mainDebug, fridaDebug bool) *Logger {
	return NewWithWriters(mainDebug, fridaDebug, os.Stdout, os.Stderr)
}
func NewWithWriters(mainDebug, fridaDebug bool, stdout, stderr io.Writer) *Logger {
	return &Logger{MainDebug: mainDebug, FridaDebug: fridaDebug, std: log.New(stdout, "", 0), err: log.New(stderr, "", 0)}
}
func (l *Logger) Info(v ...any)  { l.std.Println(v...) }
func (l *Logger) Error(v ...any) { l.err.Println(v...) }
func (l *Logger) Main(v ...any) {
	if l.MainDebug {
		l.std.Println(v...)
	}
}
func (l *Logger) Frida(v ...any) {
	if l.FridaDebug {
		l.std.Println(v...)
	}
}
