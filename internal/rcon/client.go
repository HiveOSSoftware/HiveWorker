package rcon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"time"
)

const (
	packetAuth     = 3
	packetCommand  = 2
	packetResponse = 0
)

type Client struct {
	conn net.Conn
	id   int32
}

func Dial(address string, password string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return nil, err
	}

	client := &Client{
		conn: conn,
		id:   1,
	}

	if err := client.auth(password); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return client, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) auth(password string) error {
	if err := c.writePacket(packetAuth, password); err != nil {
		return err
	}

	id, _, _, err := c.readPacket()
	if err != nil {
		return err
	}

	if id == -1 {
		return errors.New("rcon authentication failed")
	}

	return nil
}

func (c *Client) Command(command string) (string, error) {
	if err := c.writePacket(packetCommand, command); err != nil {
		return "", err
	}

	_, _, body, err := c.readPacket()
	if err != nil {
		return "", err
	}

	return body, nil
}

func (c *Client) writePacket(packetType int32, body string) error {
	c.id++

	payload := new(bytes.Buffer)

	_ = binary.Write(payload, binary.LittleEndian, c.id)
	_ = binary.Write(payload, binary.LittleEndian, packetType)
	payload.WriteString(body)
	payload.WriteByte(0)
	payload.WriteByte(0)

	size := int32(payload.Len())

	packet := new(bytes.Buffer)
	_ = binary.Write(packet, binary.LittleEndian, size)
	packet.Write(payload.Bytes())

	_, err := c.conn.Write(packet.Bytes())
	return err
}

func (c *Client) readPacket() (int32, int32, string, error) {
	var size int32

	if err := binary.Read(c.conn, binary.LittleEndian, &size); err != nil {
		return 0, 0, "", err
	}

	data := make([]byte, size)

	if _, err := c.conn.Read(data); err != nil {
		return 0, 0, "", err
	}

	buf := bytes.NewReader(data)

	var id int32
	var packetType int32

	_ = binary.Read(buf, binary.LittleEndian, &id)
	_ = binary.Read(buf, binary.LittleEndian, &packetType)

	bodyBytes := make([]byte, len(data)-10)
	_, _ = buf.Read(bodyBytes)

	body := strings.TrimRight(string(bodyBytes), "\x00")

	return id, packetType, body, nil
}
