package cfg

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	APIKey    string
	APISecret string
	Testnet   bool
	BotToken  string
	ChatID    string
}

func Load(envFile string) (Config, error) {
	loadDotEnv(envFile)
	c := Config{
		APIKey:    os.Getenv("BYBIT_API_KEY"),
		APISecret: os.Getenv("BYBIT_API_SECRET"),
		Testnet:   boolEnv(os.Getenv("BYBIT_TESTNET")),
		BotToken:  os.Getenv("TG_BOT_TOKEN"),
		ChatID:    os.Getenv("TG_CHAT_ID"),
	}
	return c, nil
}

func (c Config) Validate() error {
	var missing []string
	if c.APIKey == "" {
		missing = append(missing, "BYBIT_API_KEY")
	}
	if c.APISecret == "" {
		missing = append(missing, "BYBIT_API_SECRET")
	}
	if c.BotToken == "" {
		missing = append(missing, "TG_BOT_TOKEN")
	}
	if c.ChatID == "" {
		missing = append(missing, "TG_CHAT_ID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少必要配置项(可通过 .env 或环境变量设置): %s", strings.Join(missing, ", "))
	}
	return nil
}

func boolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		val = strings.Trim(val, `"'`)
		if _, exists := os.LookupEnv(key); !exists && key != "" {
			os.Setenv(key, val)
		}
	}
}
