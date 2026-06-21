package proxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

const (
	socksVersion = 0x05

	methodNoAuth = 0x00

	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	replySuccess           = 0x00
	replyHostUnreachable   = 0x04
	replyConnRefused       = 0x05
	replyCommandNotSupported = 0x07
)

type reader interface {
	io.Reader
	io.ByteReader
}

type bufReader struct {
	r io.Reader
	b [1]byte
}

func (b *bufReader) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *bufReader) ReadByte() (byte, error) {
	_, err := io.ReadFull(b.r, b.b[:1])
	return b.b[0], err
}

func bufioReader(r io.Reader) reader { return &bufReader{r: r} }

func socksHandshake(r reader, w io.Writer) error {
	ver, err := r.ReadByte()
	if err != nil {
		return err
	}
	if ver != socksVersion {
		return fmt.Errorf("bad socks version %d", ver)
	}
	nmethods, err := r.ReadByte()
	if err != nil {
		return err
	}
	if nmethods == 0 {
		w.Write([]byte{socksVersion, 0xff})
		return errors.New("no auth methods")
	}
	methods := make([]byte, int(nmethods))
	if _, err := io.ReadFull(r, methods); err != nil {
		return err
	}
	acceptNoAuth := false
	for _, m := range methods {
		if m == methodNoAuth {
			acceptNoAuth = true
			break
		}
	}
	if !acceptNoAuth {
		w.Write([]byte{socksVersion, 0xff})
		return errors.New("no supported auth method")
	}
	_, err = w.Write([]byte{socksVersion, methodNoAuth})
	return err
}

func readRequest(r reader, w io.Writer) (string, byte, error) {
	ver, err := r.ReadByte()
	if err != nil {
		return "", 0, err
	}
	if ver != socksVersion {
		return "", 0, fmt.Errorf("bad request version %d", ver)
	}
	cmd, err := r.ReadByte()
	if err != nil {
		return "", 0, err
	}
	if _, err := r.ReadByte(); err != nil { // RSV
		return "", 0, err
	}
	atyp, err := r.ReadByte()
	if err != nil {
		return "", 0, err
	}

	var host string
	switch atyp {
	case atypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		host = net.IP(buf).String()
	case atypDomain:
		len, err := r.ReadByte()
		if err != nil {
			return "", 0, err
		}
		buf := make([]byte, int(len))
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		host = string(buf)
	case atypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		host = net.IP(buf).String()
	default:
		writeReply(w, socksVersion, replyCommandNotSupported, atypIPv4)
		return "", 0, fmt.Errorf("unsupported atyp %d", atyp)
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(r, portBuf); err != nil {
		return "", 0, err
	}
	port := binary.BigEndian.Uint16(portBuf)

	if cmd != cmdConnect {
		writeReply(w, socksVersion, replyCommandNotSupported, atyp)
		return "", 0, fmt.Errorf("unsupported cmd %d", cmd)
	}

	return net.JoinHostPort(host, fmt.Sprintf("%d", port)), atyp, nil
}

func writeReply(w io.Writer, ver, rep, atyp byte) error {
	// Bind address is 0.0.0.0:0 — clients generally ignore it.
	reply := []byte{ver, rep, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	_, err := w.Write(reply)
	return err
}
