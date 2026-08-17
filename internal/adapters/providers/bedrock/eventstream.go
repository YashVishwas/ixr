package bedrock

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// eventStreamMessage is one decoded frame from an
// "application/vnd.amazon.eventstream" response body — the binary framing
// Bedrock's InvokeModelWithResponseStream uses instead of standard SSE.
// See AWS's event stream encoding spec:
// https://docs.aws.amazon.com/transcribe/latest/dg/event-stream.html
// (the same wire format is shared across every AWS service that streams
// this way, Bedrock included).
type eventStreamMessage struct {
	Headers map[string]string
	Payload []byte
}

var errEventStreamCRCMismatch = errors.New("bedrock: event stream CRC mismatch")

// readEventStreamMessage reads exactly one framed message from r. Returns
// io.EOF (unwrapped, checked with errors.Is by callers) when the stream
// ends cleanly between messages — i.e. there are zero bytes left to read
// a prelude from, not a truncated message mid-frame (which is a real error).
//
// Frame layout (all integers big-endian):
//
//	total length   uint32  (byte count of the entire message, prelude included)
//	headers length uint32  (byte count of the headers section only)
//	prelude CRC    uint32  (CRC32 of the two fields above)
//	headers        headers-length bytes
//	payload        (total length - 16 - headers length) bytes
//	message CRC    uint32  (CRC32 of everything from total length through payload)
func readEventStreamMessage(r io.Reader) (*eventStreamMessage, error) {
	var prelude [12]byte
	if _, err := io.ReadFull(r, prelude[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("bedrock: read event stream prelude: %w", err)
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])

	if crc32.ChecksumIEEE(prelude[0:8]) != preludeCRC {
		return nil, errEventStreamCRCMismatch
	}
	// 16 = 12-byte prelude + 4-byte trailing message CRC — the minimum
	// possible frame (empty headers, empty payload).
	if totalLen < 16 || uint64(headersLen)+16 > uint64(totalLen) {
		return nil, fmt.Errorf("bedrock: malformed event stream message (total=%d headers=%d)", totalLen, headersLen)
	}

	rest := make([]byte, totalLen-12) // headers + payload + trailing message CRC
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, fmt.Errorf("bedrock: read event stream body: %w", err)
	}

	msgCRC := binary.BigEndian.Uint32(rest[len(rest)-4:])
	full := append(append([]byte{}, prelude[:]...), rest[:len(rest)-4]...)
	if crc32.ChecksumIEEE(full) != msgCRC {
		return nil, errEventStreamCRCMismatch
	}

	headerBytes := rest[:headersLen]
	payload := rest[headersLen : len(rest)-4]

	headers, err := parseEventStreamHeaders(headerBytes)
	if err != nil {
		return nil, err
	}
	return &eventStreamMessage{Headers: headers, Payload: payload}, nil
}

// parseEventStreamHeaders decodes the repeated
// [name-len(1) name value-type(1) value] header entries that fill the
// headers section of a frame. Bedrock only ever sends string-typed headers
// (":event-type", ":message-type", ":content-type", ":exception-type") in
// practice, but every type in the spec is handled so an unexpected header
// shape degrades to a parse error instead of silently misreading the
// remaining bytes and corrupting whatever comes after it.
func parseEventStreamHeaders(b []byte) (map[string]string, error) {
	headers := make(map[string]string)
	for len(b) > 0 {
		nameLen := int(b[0])
		b = b[1:]
		if len(b) < nameLen+1 {
			return nil, fmt.Errorf("bedrock: truncated event stream header name")
		}
		name := string(b[:nameLen])
		b = b[nameLen:]
		valueType := b[0]
		b = b[1:]

		var value string
		var err error
		value, b, err = readEventStreamHeaderValue(valueType, b)
		if err != nil {
			return nil, err
		}
		headers[name] = value
	}
	return headers, nil
}

// readEventStreamHeaderValue decodes one header value of the given type
// from the front of b, returning the value (stringified for the handful of
// numeric/bool types Bedrock doesn't actually use but the spec allows) and
// the remaining unconsumed bytes.
func readEventStreamHeaderValue(valueType byte, b []byte) (value string, rest []byte, err error) {
	switch valueType {
	case 0: // bool true, no value bytes
		return "true", b, nil
	case 1: // bool false, no value bytes
		return "false", b, nil
	case 2: // byte
		if len(b) < 1 {
			return "", nil, fmt.Errorf("bedrock: truncated event stream byte header")
		}
		return fmt.Sprintf("%d", int8(b[0])), b[1:], nil
	case 3: // short
		if len(b) < 2 {
			return "", nil, fmt.Errorf("bedrock: truncated event stream short header")
		}
		return fmt.Sprintf("%d", int16(binary.BigEndian.Uint16(b[:2]))), b[2:], nil
	case 4: // int32
		if len(b) < 4 {
			return "", nil, fmt.Errorf("bedrock: truncated event stream int header")
		}
		return fmt.Sprintf("%d", int32(binary.BigEndian.Uint32(b[:4]))), b[4:], nil
	case 5, 8: // int64, timestamp (both 8-byte big-endian)
		if len(b) < 8 {
			return "", nil, fmt.Errorf("bedrock: truncated event stream long header")
		}
		return fmt.Sprintf("%d", int64(binary.BigEndian.Uint64(b[:8]))), b[8:], nil
	case 6: // byte array: uint16 length prefix + bytes
		if len(b) < 2 {
			return "", nil, fmt.Errorf("bedrock: truncated event stream byte-array header length")
		}
		n := int(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
		if len(b) < n {
			return "", nil, fmt.Errorf("bedrock: truncated event stream byte-array header")
		}
		return string(b[:n]), b[n:], nil
	case 7: // string: uint16 length prefix + UTF-8 bytes
		if len(b) < 2 {
			return "", nil, fmt.Errorf("bedrock: truncated event stream string header length")
		}
		n := int(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
		if len(b) < n {
			return "", nil, fmt.Errorf("bedrock: truncated event stream string header")
		}
		return string(b[:n]), b[n:], nil
	case 9: // UUID: 16 raw bytes
		if len(b) < 16 {
			return "", nil, fmt.Errorf("bedrock: truncated event stream uuid header")
		}
		return fmt.Sprintf("%x", b[:16]), b[16:], nil
	default:
		return "", nil, fmt.Errorf("bedrock: unknown event stream header value type %d", valueType)
	}
}
