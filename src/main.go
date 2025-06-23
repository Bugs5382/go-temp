package main

import (
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	_ "github.com/prometheus/client_golang/prometheus/promhttp"
	"go-temp/rabbitmq"
	"go-temp/utils"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type TemperatureMessage struct {
	Timestamp   string  `json:"timestamp"`
	Temperature float64 `json:"temperature"`
	Hostname    string  `json:"hostname"`
}

var (
	tempGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "sensor_temperature_celsius",
		Help: "Current temperature in Celsius reported by the DS18B20 sensor",
	})

	msgCount = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "sensor_messages_published_total",
		Help: "Total number of messages published to RabbitMQ",
	})
)

func init() {
	prometheus.MustRegister(tempGauge)
	prometheus.MustRegister(msgCount)
}

func readTemperature(sensorPath string) (float64, error) {
	data, err := os.ReadFile(sensorPath)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "YES") {
		return 0, fmt.Errorf("sensor read failed: crc check not passed")
	}
	parts := strings.Split(lines[1], "t=")
	if len(parts) != 2 {
		return 0, fmt.Errorf("unexpected format")
	}
	rawTemp, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	return float64(rawTemp) / 1000.0, nil
}

func findSensorPath() (string, error) {
	base := "/sys/bus/w1/devices/"
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "28-") {
			return filepath.Join(base, e.Name(), "w1_slave"), nil
		}
	}
	return "", fmt.Errorf("no DS18B20 found")
}

func getEnvBool(key string, defaultVal bool) bool {
	val, exists := os.LookupEnv(key)
	if !exists {
		return defaultVal
	}
	parsed, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return parsed
}

var healthy = true

func main() {

	err := utils.SetupFileLogger("log/ms-temp-sensor.log")
	if err != nil {
		log.Fatalf("Failed to set up log file: %v", err)
	}

	hostname, _ := os.Hostname()
	sensorPath, err := findSensorPath()
	if err != nil {
		log.Fatalf("Sensor not found: %v", err)
	}

	localURI := os.Getenv("RABBITMQ_LOCAL_URI")
	remoteURI := os.Getenv("RABBITMQ_REMOTE_URI")
	queue := os.Getenv("RABBITMQ_QUEUE")
	if queue == "" {
		queue = "temp"
	}

	useMTLS := getEnvBool("RABBITMQ_USE_MTLS", false)
	useTLS := getEnvBool("RABBITMQ_USE_TLS", false)

	tlsCfg, err := rabbitmq.LoadTLSConfig(useTLS, useMTLS)
	if err != nil {
		log.Fatalf("TLS config error: %v", err)
	}

	type clientRef struct {
		name   string
		client *rabbitmq.Client
	}

	var clients []clientRef

	if localURI != "" {
		client, err := rabbitmq.NewClient(localURI, queue, tlsCfg)
		if err != nil {
			log.Printf("Local RabbitMQ connection failed: %v", err)
		} else {
			clients = append(clients, clientRef{"local", client})
		}
	}

	if remoteURI != "" {
		client, err := rabbitmq.NewClient(remoteURI, queue, tlsCfg)
		if err != nil {
			log.Printf("Remote RabbitMQ connection failed: %v", err)
		} else {
			clients = append(clients, clientRef{"remote", client})
		}
	}

	if len(clients) == 0 {
		log.Fatal("No RabbitMQ connections available")
	}

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			if healthy {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("OK"))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
		})
		log.Println("Serving /metrics and /healthz on :8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			temp, err := readTemperature(sensorPath)
			if err != nil {
				log.Printf("Failed to read sensor: %v", err)
				continue
			}
			msg := TemperatureMessage{
				Timestamp:   time.Now().Format(time.RFC3339),
				Temperature: temp,
				Hostname:    hostname,
			}

			tempGauge.Set(temp)
			msgCount.Inc()

			for _, c := range clients {
				if err := c.client.PublishJSON(msg); err != nil {
					log.Printf("Failed to publish to %s: %v", c.name, err)
				}
			}
		case <-sigChan:
			log.Println("Shutting down")
			healthy = false
			for _, c := range clients {
				c.client.Close()
			}
			return
		}
	}
}
