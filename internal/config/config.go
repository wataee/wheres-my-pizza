package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Database struct {
		Host     string
		Port     int
		User     string
		Password string
		DBName   string
	}

	RabbitMQ struct {
		Host     string
		Port     int
		User     string
		Password string
	}
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	cfg := &Config{}
	scanner := bufio.NewScanner(file)
	
	currentSection := ""

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if line == "database:" {
			currentSection = "database"
			continue
		}
		if line == "rabbitmq:" {
			currentSection = "rabbitmq"
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch currentSection {
		case "database":
			switch key {
			case "host":
				cfg.Database.Host = val
			case "port":
				port, _ := strconv.Atoi(val)
				cfg.Database.Port = port
			case "user":
				cfg.Database.User = val
			case "password":
				cfg.Database.Password = val
			case "database":
				cfg.Database.DBName = val
			}
		case "rabbitmq":
			switch key {
			case "host":
				cfg.RabbitMQ.Host = val
			case "port":
				port, _ := strconv.Atoi(val)
				cfg.RabbitMQ.Port = port
			case "user":
				cfg.RabbitMQ.User = val
			case "password":
				cfg.RabbitMQ.Password = val
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	return cfg, nil
}

func (c *Config) DBConnectionURL() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		c.Database.User,
		c.Database.Password,
		c.Database.Host,
		c.Database.Port,
		c.Database.DBName,
	)
}

func (c *Config) RabbitMQURL() string {
	return fmt.Sprintf("amqp://%s:%s@%s:%d/",
		c.RabbitMQ.User,
		c.RabbitMQ.Password,
		c.RabbitMQ.Host,
		c.RabbitMQ.Port,
	)
}