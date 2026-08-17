package bedrock

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"testing"
)

// encodeEventStreamMessage builds a valid AWS event-stream frame for the
// given headers and payload — the inverse of readEventStreamMessage, used
// only in these tests to construct fixtures with correct CRCs, since
// hand-computing CRC32 values would be error-prone and wouldn't actually
// prove the reader validates them correctly.
func encodeEventStreamMessage(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()

	var headerBytes bytes.Buffer
	for name, value := range headers {
		headerBytes.WriteByte(byte(len(name)))
		headerBytes.WriteString(name)
		headerBytes.WriteByte(7) // string type
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(value)))
		headerBytes.Write(lenBuf[:])
		headerBytes.WriteString(value)
	}

	totalLen := uint32(12 + headerBytes.Len() + len(payload) + 4)

	var prelude [8]byte
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], uint32(headerBytes.Len()))
	preludeCRC := crc32.ChecksumIEEE(prelude[:])

	var buf bytes.Buffer
	buf.Write(prelude[:])
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], preludeCRC)
	buf.Write(crcBuf[:])
	buf.Write(headerBytes.Bytes())
	buf.Write(payload)

	msgCRC := crc32.ChecksumIEEE(buf.Bytes())
	binary.BigEndian.PutUint32(crcBuf[:], msgCRC)
	buf.Write(crcBuf[:])

	return buf.Bytes()
}

func TestReadEventStreamMessage_RoundTrip(t *testing.T) {
	headers := map[string]string{":event-type": "chunk", ":content-type": "application/json"}
	payload := []byte(`{"bytes":"eyJmb28iOiJiYXIifQ=="}`)
	encoded := encodeEventStreamMessage(t, headers, payload)

	msg, err := readEventStreamMessage(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Headers[":event-type"] != "chunk" || msg.Headers[":content-type"] != "application/json" {
		t.Errorf("headers: got %+v", msg.Headers)
	}
	if !bytes.Equal(msg.Payload, payload) {
		t.Errorf("payload: got %q, want %q", msg.Payload, payload)
	}
}

func TestReadEventStreamMessage_MultipleMessagesInSequence(t *testing.T) {
	msg1 := encodeEventStreamMessage(t, map[string]string{":event-type": "chunk"}, []byte(`{"a":1}`))
	msg2 := encodeEventStreamMessage(t, map[string]string{":event-type": "chunk"}, []byte(`{"b":2}`))
	r := bytes.NewReader(append(msg1, msg2...))

	got1, err := readEventStreamMessage(r)
	if err != nil {
		t.Fatalf("first message: unexpected error: %v", err)
	}
	if string(got1.Payload) != `{"a":1}` {
		t.Errorf("first payload: got %q", got1.Payload)
	}

	got2, err := readEventStreamMessage(r)
	if err != nil {
		t.Fatalf("second message: unexpected error: %v", err)
	}
	if string(got2.Payload) != `{"b":2}` {
		t.Errorf("second payload: got %q", got2.Payload)
	}

	if _, err := readEventStreamMessage(r); !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF after both messages consumed, got %v", err)
	}
}

func TestReadEventStreamMessage_CleanEOFOnEmptyStream(t *testing.T) {
	if _, err := readEventStreamMessage(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF on an empty stream, got %v", err)
	}
}

func TestReadEventStreamMessage_TruncatedMidFrame_IsARealError(t *testing.T) {
	full := encodeEventStreamMessage(t, map[string]string{":event-type": "chunk"}, []byte(`{"a":1}`))
	truncated := full[:len(full)-5] // cut off mid-payload/CRC, not at a clean boundary

	_, err := readEventStreamMessage(bytes.NewReader(truncated))
	if err == nil {
		t.Fatal("expected an error for a mid-frame truncation")
	}
	if errors.Is(err, io.EOF) {
		t.Error("a mid-frame truncation must not present as a clean io.EOF — that would silently drop the tail of a real response")
	}
}

func TestReadEventStreamMessage_CorruptedPreludeCRC_Rejected(t *testing.T) {
	encoded := encodeEventStreamMessage(t, map[string]string{":event-type": "chunk"}, []byte(`{}`))
	corrupted := append([]byte(nil), encoded...)
	corrupted[0] ^= 0xFF // flip a bit in the total-length field, invalidating the prelude CRC

	_, err := readEventStreamMessage(bytes.NewReader(corrupted))
	if !errors.Is(err, errEventStreamCRCMismatch) {
		t.Errorf("expected errEventStreamCRCMismatch, got %v", err)
	}
}

func TestReadEventStreamMessage_CorruptedPayload_MessageCRCRejected(t *testing.T) {
	encoded := encodeEventStreamMessage(t, map[string]string{":event-type": "chunk"}, []byte(`{"a":1}`))
	corrupted := append([]byte(nil), encoded...)
	// Flip a byte inside the payload — prelude CRC (computed only over the
	// two length fields) stays valid; the whole-message CRC must not.
	corrupted[len(corrupted)-6] ^= 0xFF

	_, err := readEventStreamMessage(bytes.NewReader(corrupted))
	if !errors.Is(err, errEventStreamCRCMismatch) {
		t.Errorf("expected errEventStreamCRCMismatch, got %v", err)
	}
}

func TestReadEventStreamMessage_MultipleHeaderTypes(t *testing.T) {
	// encodeEventStreamMessage only emits string-typed headers (7), which
	// covers everything Bedrock actually sends — this test exercises the
	// other value-type branches of readEventStreamHeaderValue directly,
	// since a real Bedrock stream will never exercise them.
	var headerBytes bytes.Buffer
	writeHeader := func(name string, valueType byte, value []byte) {
		headerBytes.WriteByte(byte(len(name)))
		headerBytes.WriteString(name)
		headerBytes.WriteByte(valueType)
		headerBytes.Write(value)
	}
	writeHeader("bool-true", 0, nil)
	writeHeader("bool-false", 1, nil)
	writeHeader("a-byte", 2, []byte{42})

	headers, err := parseEventStreamHeaders(headerBytes.Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if headers["bool-true"] != "true" || headers["bool-false"] != "false" || headers["a-byte"] != "42" {
		t.Errorf("got %+v", headers)
	}
}

func TestReadEventStreamHeaderValue_UnknownType_Errors(t *testing.T) {
	if _, _, err := readEventStreamHeaderValue(0xFF, nil); err == nil {
		t.Error("expected an error for an unrecognized header value type")
	}
}
