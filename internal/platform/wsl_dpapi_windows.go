//go:build windows

package platform

import (
	"bytes"
	"io"
)

// RunWSLDPAPIHelper serves one size-bounded request on stdin and writes one
// response on stdout. The caller supplies key bytes only through the pipe.
func RunWSLDPAPIHelper(input io.Reader, output io.Writer) error {
	request, err := decodeWSLHelperRequest(io.LimitReader(input, wslProtocolMaxPayload+256))
	if err != nil || !validWSLGeneration(request.generation) {
		return writeWSLHelperFailure(output)
	}
	defer clear(request.payload)
	var result []byte
	if request.operation == wslOperationProtect {
		result, err = protectDPAPI(request.payload, []byte(request.generation))
	} else {
		result, err = unprotectDPAPI(request.payload, []byte(request.generation))
	}
	if err != nil {
		return writeWSLHelperFailure(output)
	}
	defer clear(result)
	response, err := encodeWSLHelperResponse(0, result)
	if err != nil {
		return err
	}
	defer clear(response)
	_, err = io.Copy(output, bytes.NewReader(response))
	return err
}

func writeWSLHelperFailure(output io.Writer) error {
	response, _ := encodeWSLHelperResponse(1, nil)
	_, err := output.Write(response)
	return err
}
