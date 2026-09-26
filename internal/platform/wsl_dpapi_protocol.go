package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const (
	wslHelperName              = "codex-folio-wsl-vault.exe"
	wslProtocolVersion    byte = 1
	wslOperationProtect   byte = 1
	wslOperationUnprotect byte = 2
	wslProtocolMaxPayload      = 64 * 1024
)

var errWSLProtocol = errors.New("WSL vault helper protocol is invalid")

type wslHelperRequest struct {
	operation  byte
	generation string
	payload    []byte
}

func encodeWSLHelperRequest(operation byte, generation string, payload []byte) ([]byte, error) {
	if (operation != wslOperationProtect && operation != wslOperationUnprotect) || !validWSLGeneration(generation) || len(payload) == 0 || len(payload) > wslProtocolMaxPayload {
		return nil, errWSLProtocol
	}
	result := make([]byte, 12+len(generation)+len(payload))
	copy(result, "CFWQ")
	result[4] = wslProtocolVersion
	result[5] = operation
	binary.BigEndian.PutUint16(result[6:8], uint16(len(generation)))
	binary.BigEndian.PutUint32(result[8:12], uint32(len(payload)))
	copy(result[12:], generation)
	copy(result[12+len(generation):], payload)
	return result, nil
}

func decodeWSLHelperRequest(reader io.Reader) (wslHelperRequest, error) {
	header := make([]byte, 12)
	if _, err := io.ReadFull(reader, header); err != nil || !bytes.Equal(header[:4], []byte("CFWQ")) || header[4] != wslProtocolVersion {
		return wslHelperRequest{}, errWSLProtocol
	}
	generationSize := int(binary.BigEndian.Uint16(header[6:8]))
	payloadSize := int(binary.BigEndian.Uint32(header[8:12]))
	if generationSize != 32 || payloadSize == 0 || payloadSize > wslProtocolMaxPayload || (header[5] != wslOperationProtect && header[5] != wslOperationUnprotect) {
		return wslHelperRequest{}, errWSLProtocol
	}
	body := make([]byte, generationSize+payloadSize)
	if _, err := io.ReadFull(reader, body); err != nil {
		return wslHelperRequest{}, errWSLProtocol
	}
	if extra, _ := io.ReadAll(io.LimitReader(reader, 1)); len(extra) != 0 {
		clear(body)
		return wslHelperRequest{}, errWSLProtocol
	}
	generation := string(body[:generationSize])
	if !validWSLGeneration(generation) {
		clear(body)
		return wslHelperRequest{}, errWSLProtocol
	}
	return wslHelperRequest{operation: header[5], generation: generation, payload: body[generationSize:]}, nil
}

func validWSLGeneration(generation string) bool {
	if len(generation) != 32 {
		return false
	}
	for _, current := range generation {
		if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
			return false
		}
	}
	return true
}

func encodeWSLHelperResponse(status byte, payload []byte) ([]byte, error) {
	if status > 1 || len(payload) > wslProtocolMaxPayload || (status == 0 && len(payload) == 0) {
		return nil, errWSLProtocol
	}
	result := make([]byte, 10+len(payload))
	copy(result, "CFWR")
	result[4] = wslProtocolVersion
	result[5] = status
	binary.BigEndian.PutUint32(result[6:10], uint32(len(payload)))
	copy(result[10:], payload)
	return result, nil
}

func decodeWSLHelperResponse(data []byte) ([]byte, error) {
	if len(data) < 10 || !bytes.Equal(data[:4], []byte("CFWR")) || data[4] != wslProtocolVersion || data[5] != 0 {
		return nil, errWSLProtocol
	}
	size := int(binary.BigEndian.Uint32(data[6:10]))
	if size == 0 || size > wslProtocolMaxPayload || len(data) != 10+size {
		return nil, errWSLProtocol
	}
	return bytes.Clone(data[10:]), nil
}
