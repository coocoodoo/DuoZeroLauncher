package main

// A small WebSocket (RFC 6455) for PictoChat, standard library only: text frames, ping/pong and
// close, as a client (ws:// and wss://) and as a server (an HTTP upgrade). Outgoing messages are
// single frames; incoming fragmented messages are joined. Kept small on purpose: no extensions.

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
)

const wsMaxMessage = 512 << 10 // the same limit the PictoChat server uses

type wsConn struct {
	c      net.Conn
	r      *bufio.Reader
	client bool // a client masks what it sends
	wmu    sync.Mutex
}

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}

// wsURL turns what the user typed ("host", "host:port", "ws://host/path") into a ws:// URL.
func wsURL(addr string, defPort int) (*url.URL, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("no address")
	}
	if !strings.Contains(addr, "://") {
		addr = "ws://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("can't use %s://", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("no host in the address")
	}
	if u.Port() == "" {
		port := defPort
		if u.Scheme == "wss" {
			port = 443
		}
		u.Host = net.JoinHostPort(u.Hostname(), fmt.Sprint(port))
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

// wsDial opens a client connection.
func wsDial(u *url.URL, timeout time.Duration) (*wsConn, error) {
	d := &net.Dialer{Timeout: timeout}
	var c net.Conn
	var err error
	if u.Scheme == "wss" {
		c, err = tls.DialWithDialer(d, "tcp", u.Host, &tls.Config{ServerName: u.Hostname()})
	} else {
		c, err = d.Dial("tcp", u.Host)
	}
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(timeout))
	var k [16]byte
	rand.Read(k[:])
	key := base64.StdEncoding.EncodeToString(k[:])
	origin := "http://" + u.Host
	if u.Scheme == "wss" {
		origin = "https://" + u.Host
	}
	req := "GET " + u.RequestURI() + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\n" +
		"Connection: Upgrade\r\nSec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n" +
		"Origin: " + origin + "\r\nUser-Agent: DuoLauncher\r\n\r\n"
	if _, err := io.WriteString(c, req); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	tp := textproto.NewReader(br)
	status, err := tp.ReadLine()
	if err != nil {
		c.Close()
		return nil, err
	}
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		c.Close()
		return nil, err
	}
	if f := strings.Fields(status); len(f) < 2 || f[1] != "101" {
		c.Close()
		return nil, fmt.Errorf("the server said %q", status)
	}
	if hdr.Get("Sec-WebSocket-Accept") != wsAccept(key) {
		c.Close()
		return nil, errors.New("not a WebSocket server")
	}
	c.SetDeadline(time.Time{})
	return &wsConn{c: c, r: br, client: true}, nil
}

// wsServe reads an HTTP upgrade request from a new connection and answers it, giving the
// server side of a WebSocket. Anything else gets a short refusal. (Done by hand rather than
// with net/http, which would add megabytes to Duo for one handshake.)
func wsServe(c net.Conn) (*wsConn, error) {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(c)
	tp := textproto.NewReader(br)
	line, err := tp.ReadLine()
	if err != nil {
		return nil, err
	}
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	key := hdr.Get("Sec-WebSocket-Key")
	if !strings.HasPrefix(line, "GET ") || !strings.EqualFold(hdr.Get("Upgrade"), "websocket") || key == "" {
		msg := "Duo PictoChat: connect with a WebSocket\n"
		io.WriteString(c, fmt.Sprintf("HTTP/1.1 426 Upgrade Required\r\nContent-Type: text/plain\r\n"+
			"Content-Length: %d\r\nConnection: close\r\n\r\n%s", len(msg), msg))
		return nil, errors.New("not a WebSocket request")
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAccept(key) + "\r\n\r\n"
	if _, err := io.WriteString(c, resp); err != nil {
		return nil, err
	}
	c.SetDeadline(time.Time{})
	return &wsConn{c: c, r: br}, nil
}

// Read returns the next text message. It answers pings itself and returns io.EOF when the other
// side closes.
func (w *wsConn) Read() (string, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.r, h[:]); err != nil {
			return "", err
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		masked := h[1]&0x80 != 0
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return "", err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return "", err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > wsMaxMessage || uint64(len(msg))+n > wsMaxMessage {
			return "", errors.New("message too big")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.r, mask[:]); err != nil {
				return "", err
			}
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.r, p); err != nil {
			return "", err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i&3]
			}
		}
		switch op {
		case 0x8: // close
			w.writeFrame(0x8, nil)
			return "", io.EOF
		case 0x9: // ping
			if err := w.writeFrame(0xA, p); err != nil {
				return "", err
			}
		case 0xA: // pong
		case 0x0, 0x1, 0x2:
			msg = append(msg, p...)
			if fin {
				return string(msg), nil
			}
		default:
			return "", fmt.Errorf("unknown frame %d", op)
		}
	}
}

func (w *wsConn) writeFrame(op byte, p []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	buf := make([]byte, 0, len(p)+14)
	buf = append(buf, 0x80|op)
	m := byte(0)
	if w.client {
		m = 0x80
	}
	switch n := len(p); {
	case n < 126:
		buf = append(buf, m|byte(n))
	case n <= 0xffff:
		buf = append(buf, m|126, byte(n>>8), byte(n))
	default:
		buf = append(buf, m|127)
		buf = binary.BigEndian.AppendUint64(buf, uint64(n))
	}
	if w.client {
		var k [4]byte
		rand.Read(k[:])
		buf = append(buf, k[:]...)
		start := len(buf)
		buf = append(buf, p...)
		for i := range p {
			buf[start+i] ^= k[i&3]
		}
	} else {
		buf = append(buf, p...)
	}
	w.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := w.c.Write(buf)
	return err
}

func (w *wsConn) WriteText(s string) error { return w.writeFrame(0x1, []byte(s)) }

func (w *wsConn) Close() error {
	w.writeFrame(0x8, nil)
	return w.c.Close()
}
