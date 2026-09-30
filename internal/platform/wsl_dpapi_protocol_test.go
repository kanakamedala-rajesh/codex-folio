package platform

import (
	"bytes"
	"testing"
)

func TestWSLHelperProtocolRejectsMalformedFrames(t *testing.T) {
	generation := "0123456789abcdef0123456789abcdef"
	request, err := encodeWSLHelperRequest(wslOperationProtect, generation, []byte("private key bytes"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeWSLHelperRequest(bytes.NewReader(request))
	if err != nil || decoded.operation != wslOperationProtect || decoded.generation != generation || string(decoded.payload) != "private key bytes" {
		t.Fatalf("decoded request = %#v, %v", decoded, err)
	}
	for name, malformed := range map[string][]byte{
		"trailing":             append(bytes.Clone(request), 0),
		"uppercase-generation": bytes.Replace(bytes.Clone(request), []byte(generation), []byte("0123456789ABCDEF0123456789ABCDEF"), 1),
		"bad-version":          append(bytes.Clone(request[:4]), append([]byte{9}, request[5:]...)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeWSLHelperRequest(bytes.NewReader(malformed)); err == nil {
				t.Fatal("malformed request was accepted")
			}
		})
	}
}

func TestWSLHelperResponseIsSizeBounded(t *testing.T) {
	response, err := encodeWSLHelperResponse(0, []byte("protected"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decodeWSLHelperResponse(response); err != nil || string(got) != "protected" {
		t.Fatalf("response = %q, %v", got, err)
	}
	if _, err := decodeWSLHelperResponse(append(response, 0)); err == nil {
		t.Fatal("trailing response data was accepted")
	}
}
