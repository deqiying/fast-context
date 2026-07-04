package protowire

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf8"
)

type Encoder struct {
	buf []byte
}

func (e *Encoder) Varint(field int, value uint64) {
	e.buf = append(e.buf, encodeVarint(uint64(field<<3))...)
	e.buf = append(e.buf, encodeVarint(value)...)
}

func (e *Encoder) String(field int, value string) {
	e.Bytes(field, []byte(value))
}

func (e *Encoder) Bytes(field int, value []byte) {
	e.buf = append(e.buf, encodeVarint(uint64(field<<3|2))...)
	e.buf = append(e.buf, encodeVarint(uint64(len(value)))...)
	e.buf = append(e.buf, value...)
}

func (e *Encoder) Message(field int, sub *Encoder) {
	e.Bytes(field, sub.BytesValue())
}

func (e *Encoder) BytesValue() []byte {
	out := make([]byte, len(e.buf))
	copy(out, e.buf)
	return out
}

func encodeVarint(value uint64) []byte {
	var out []byte
	for value > 0x7f {
		out = append(out, byte(value&0x7f|0x80))
		value >>= 7
	}
	out = append(out, byte(value))
	return out
}

func DecodeVarint(buf []byte, offset int) (uint64, int, error) {
	var value uint64
	var shift uint
	for offset < len(buf) {
		b := buf[offset]
		offset++
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, offset, nil
		}
		shift += 7
		if shift > 63 {
			return 0, offset, errors.New("varint overflow")
		}
	}
	return 0, offset, io.ErrUnexpectedEOF
}

func ExtractStrings(data []byte) []string {
	var strings []string
	i := 0
	for i < len(data) {
		tag, next, err := DecodeVarint(data, i)
		if err != nil {
			break
		}
		i = next
		wire := tag & 0x7
		switch wire {
		case 0:
			_, next, err := DecodeVarint(data, i)
			if err != nil {
				return strings
			}
			i = next
		case 1:
			i += 8
		case 2:
			length, next, err := DecodeVarint(data, i)
			if err != nil {
				return strings
			}
			i = next
			end := i + int(length)
			if end > len(data) {
				return strings
			}
			raw := data[i:end]
			if len(raw) > 5 && utf8.Valid(raw) {
				strings = append(strings, string(raw))
			}
			i = end
		case 5:
			i += 4
		default:
			return strings
		}
		if i < 0 || i > len(data) {
			return strings
		}
	}
	return strings
}

func EncodeConnectFrame(proto []byte, compress bool) ([]byte, error) {
	payload := proto
	flags := byte(0)
	if compress {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		if _, err := gz.Write(proto); err != nil {
			return nil, err
		}
		if err := gz.Close(); err != nil {
			return nil, err
		}
		payload = b.Bytes()
		flags = 1
	}
	out := make([]byte, 5+len(payload))
	out[0] = flags
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out, nil
}

func DecodeConnectFrames(data []byte) ([][]byte, error) {
	var frames [][]byte
	for i := 0; i+5 <= len(data); {
		flags := data[i]
		length := int(binary.BigEndian.Uint32(data[i+1 : i+5]))
		i += 5
		if length < 0 || i+length > len(data) {
			return frames, io.ErrUnexpectedEOF
		}
		payload := append([]byte(nil), data[i:i+length]...)
		i += length
		if flags == 1 || flags == 3 {
			decoded, err := gunzip(payload)
			if err == nil {
				payload = decoded
			}
		}
		frames = append(frames, payload)
	}
	return frames, nil
}

func gunzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
