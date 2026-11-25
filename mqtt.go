package main

// A small MQTT 3.1.1 client, standard library only, for PictoChat's internet rooms: connect
// (TLS or plain TCP, with a last will), subscribe, publish at QoS 0, keep-alive pings. Received
// messages go to a callback; QoS 1 deliveries are acknowledged.

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const mqttMaxPacket = 1 << 20

type mqttClient struct {
	c      net.Conn
	r      *bufio.Reader
	wmu    sync.Mutex
	nextID uint16
	done   chan struct{}
	once   sync.Once
}

// mqttWill is published by the broker for us if we vanish without saying goodbye.
type mqttWill struct {
	Topic   string
	Payload []byte
}

func mqttString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}

func mqttBytes(b []byte, p []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(p)))
	return append(b, p...)
}

// mqttPacket frames a packet: type and flags, the remaining length as a varint, the body.
func mqttPacket(head byte, body []byte) []byte {
	out := []byte{head}
	n := len(body)
	for {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		out = append(out, d)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}

func mqttRead(r *bufio.Reader) (byte, []byte, error) {
	head, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, mul := 0, 1
	for i := 0; ; i++ {
		d, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n += int(d&0x7f) * mul
		if d&0x80 == 0 {
			break
		}
		if mul *= 128; i >= 3 {
			return 0, nil, errors.New("bad packet length")
		}
	}
	if n > mqttMaxPacket {
		return 0, nil, errors.New("packet too big")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return head, body, nil
}

// mqttDial connects and logs in. useTLS checks the broker's certificate against the system's.
func mqttDial(host string, port int, useTLS bool, clientID string, keepAlive time.Duration, will *mqttWill) (*mqttClient, error) {
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	d := &net.Dialer{Timeout: 8 * time.Second}
	var c net.Conn
	var err error
	if useTLS {
		c, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		c, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	body := mqttString(nil, "MQTT")
	flags := byte(0x02) // clean session
	if will != nil {
		flags |= 0x04
	}
	body = append(body, 4, flags)
	body = binary.BigEndian.AppendUint16(body, uint16(keepAlive/time.Second))
	body = mqttString(body, clientID)
	if will != nil {
		body = mqttString(body, will.Topic)
		body = mqttBytes(body, will.Payload)
	}
	if _, err := c.Write(mqttPacket(0x10, body)); err != nil {
		c.Close()
		return nil, err
	}
	r := bufio.NewReader(c)
	head, ack, err := mqttRead(r)
	if err != nil {
		c.Close()
		return nil, err
	}
	if head>>4 != 2 || len(ack) < 2 {
		c.Close()
		return nil, errors.New("not an MQTT broker")
	}
	if ack[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("the broker refused (code %d)", ack[1])
	}
	c.SetDeadline(time.Time{})
	return &mqttClient{c: c, r: r, done: make(chan struct{})}, nil
}

func (m *mqttClient) write(p []byte) error {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	m.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := m.c.Write(p)
	return err
}

func (m *mqttClient) Subscribe(filter string) error {
	m.nextID++
	body := binary.BigEndian.AppendUint16(nil, m.nextID)
	body = mqttString(body, filter)
	body = append(body, 0) // QoS 0
	return m.write(mqttPacket(0x82, body))
}

func (m *mqttClient) Publish(topic string, payload []byte) error {
	return m.write(mqttPacket(0x30, append(mqttString(nil, topic), payload...)))
}

// Run reads until the connection ends, handing each received message to got and pinging the
// broker every keepAlive/2. It returns why it stopped (nil after Close).
func (m *mqttClient) Run(keepAlive time.Duration, got func(topic string, payload []byte)) error {
	go func() {
		t := time.NewTicker(keepAlive / 2)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if m.write([]byte{0xC0, 0}) != nil {
					m.c.Close()
					return
				}
			case <-m.done:
				return
			}
		}
	}()
	for {
		m.c.SetReadDeadline(time.Now().Add(keepAlive * 2))
		head, body, err := mqttRead(m.r)
		if err != nil {
			select {
			case <-m.done:
				return nil
			default:
				return err
			}
		}
		if head>>4 != 3 { // only PUBLISH matters; SUBACK, PINGRESP... are fine as they are
			continue
		}
		if len(body) < 2 {
			continue
		}
		tl := int(binary.BigEndian.Uint16(body))
		if len(body) < 2+tl {
			continue
		}
		topic, rest := string(body[2:2+tl]), body[2+tl:]
		if qos := head >> 1 & 3; qos > 0 {
			if len(rest) < 2 {
				continue
			}
			if qos == 1 {
				m.write(mqttPacket(0x40, rest[:2])) // PUBACK
			}
			rest = rest[2:]
		}
		got(topic, rest)
	}
}

// Close says goodbye (so the broker drops our last will) and hangs up.
func (m *mqttClient) Close() {
	m.once.Do(func() {
		close(m.done)
		m.write([]byte{0xE0, 0})
		m.c.Close()
	})
}
