//go:build windows && frida

package wmpf

import fridacore "github.com/Follen/miniapp-bridge/internal/frida"

func zlibCompress(data []byte) ([]byte, error) {
	return fridacore.ZlibCompress(data)
}

func zlibDecompress(data []byte) ([]byte, error) {
	return fridacore.ZlibDecompress(data, 0)
}

func ZlibVersion() string { return "1.3.1" }
