package influxdb_client

import (
	"fmt"
	"io"
	"net"
	"time"
)

const (
	UDPPayloadSize = 512
)

type UDPConfig struct {
	Addr        string
	PayloadSize int
}

func NewUDPClient(conf UDPConfig) (Client, error) {
	var udpAddr *net.UDPAddr
	udpAddr, err := net.ResolveUDPAddr("udp", conf.Addr)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, err
	}

	payloadSize := conf.PayloadSize
	if payloadSize == 0 {
		payloadSize = UDPPayloadSize
	}

	return &udpclient{
		conn:        conn,
		payloadSize: payloadSize,
	}, nil
}

func (uc *udpclient) Close() error {
	return uc.conn.Close()
}

type udpclient struct {
	conn        io.WriteCloser
	payloadSize int
}

func (uc *udpclient) Write(bp BatchPoints) error {
	var b = make([]byte, 0, uc.payloadSize)
	var d, _ = time.ParseDuration("1" + bp.Precision())

	var delayedError error

	var checkBuffer = func(n int) {
		if len(b) > 0 && len(b)+n > uc.payloadSize {
			if _, err := uc.conn.Write(b); err != nil {
				delayedError = err
			}
			b = b[:0]
		}
	}

	for _, p := range bp.Points() {
		p.pt.Round(d)
		pointSize := p.pt.StringSize() + 1
		checkBuffer(pointSize)
		if p.Time().IsZero() || pointSize <= uc.payloadSize {
			b = p.pt.AppendString(b)
			b = append(b, '\n')
			continue
		}
		points := p.pt.Split(uc.payloadSize - 1)
		for _, sp := range points {
			checkBuffer(sp.StringSize() + 1)
			b = sp.AppendString(b)
			b = append(b, '\n')
		}
	}

	if len(b) > 0 {
		if _, err := uc.conn.Write(b); err != nil {
			return err
		}
	}
	return delayedError
}

func (uc *udpclient) Query(q Query) (*Response, error) {
	return nil, fmt.Errorf("Querying via UDP is not supported")
}

func (uc *udpclient) QueryFromDatabase(q Query) (int64, *Response, error) {
	return 0, nil, nil
}

func (uc *udpclient) QueryAsChunk(q Query) (*ChunkedResponse, error) {
	return nil, fmt.Errorf("Querying via UDP is not supported")
}

func (uc *udpclient) Ping(timeout time.Duration) (time.Duration, string, error) {
	return 0, "", nil
}
