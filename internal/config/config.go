package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen          string   `yaml:"listen"`
	WebListen       string   `yaml:"web_listen"`
	UpstreamProxies []string `yaml:"upstream_proxies"`
	LogIPs          bool     `yaml:"log_ips"`
}

func Default() *Config {
	return &Config{
		Listen:    ":1080",
		WebListen: ":8080",
		LogIPs:    false,
	}
}

func Load(path string) (*Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(c.UpstreamProxies) == 0 {
		return nil, fmt.Errorf("no upstream_proxies configured")
	}
	return c, nil
}
