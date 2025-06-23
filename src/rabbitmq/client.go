package rabbitmq

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Client struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	queue   string
	uri     string
	tlsCfg  *tls.Config
}

func NewClient(uri, queue string, tlsCfg *tls.Config) (*Client, error) {
	client := &Client{uri: uri, queue: queue, tlsCfg: tlsCfg}
	if err := client.connect(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Client) connect() error {
	var conn *amqp.Connection
	var err error

	if c.tlsCfg != nil {
		conn, err = amqp.DialTLS(c.uri, c.tlsCfg)
	} else {
		conn, err = amqp.Dial(c.uri)
	}
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("channel failed: %w", err)
	}

	_, err = ch.QueueDeclare(
		c.queue, true, false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("queue declare failed: %w", err)
	}

	c.conn = conn
	c.channel = ch
	return nil
}

func (c *Client) PublishJSON(payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	err = c.channel.Publish("", c.queue, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
	if err != nil && errors.Is(err, amqp.ErrClosed) {
		// try to reconnect with backoff
		return c.reconnectWithBackoff(payload)
	}
	return err
}

func (c *Client) reconnectWithBackoff(payload any) error {
	backoff := time.Second
	for i := 0; i < 5; i++ {
		time.Sleep(backoff)
		err := c.connect()
		if err == nil {
			return c.PublishJSON(payload) // retry once connected
		}
		backoff *= 2
	}
	return fmt.Errorf("unable to reconnect to RabbitMQ")
}

func (c *Client) Close() {
	if c.channel != nil {
		_ = c.channel.Close()
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func LoadTLSConfig(useTLS, useMTLS bool) (*tls.Config, error) {
	if !useTLS {
		return nil, nil
	}

	cfg := &tls.Config{}

	if useMTLS {
		cert, err := tls.LoadX509KeyPair("/etc/certs/client.crt", "/etc/certs/client.key")
		if err != nil {
			return nil, fmt.Errorf("failed to load client certs: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	caCert, err := os.ReadFile("/etc/certs/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		return nil, errors.New("failed to append CA cert")
	}

	cfg.RootCAs = caPool
	cfg.MinVersion = tls.VersionTLS12
	return cfg, nil
}
