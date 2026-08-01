package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Settings struct {
	BotToken        string
	ChatID          string
	MessageThreadID *int64
	TimezoneName    string
	Location        *time.Location
	ReportHours     []int
	GraceMinutes    int
	QuoteAssets     []string
	TopN            int
	BinanceBaseURL  string
	Timeout         time.Duration
	MaxRetries      int
	StateFile       string
}

func LoadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("打开环境文件 %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			first, last := value[0], value[len(value)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("设置环境变量 %s: %w", key, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取环境文件 %s: %w", path, err)
	}
	return nil
}

func FromEnv(projectDir string) (Settings, error) {
	timezoneName := envOr("REPORT_TIMEZONE", "Asia/Shanghai")
	location, err := time.LoadLocation(timezoneName)
	if err != nil {
		return Settings{}, fmt.Errorf("无效时区 %q: %w", timezoneName, err)
	}

	hours, err := parseHours(envOr("REPORT_HOURS", "0,4,8,12,16,20"))
	if err != nil {
		return Settings{}, err
	}
	grace, err := positiveInt("SCHEDULE_GRACE_MINUTES", 10)
	if err != nil {
		return Settings{}, err
	}
	topN, err := positiveInt("TOP_N", 5)
	if err != nil {
		return Settings{}, err
	}
	timeoutSeconds, err := positiveInt("HTTP_TIMEOUT_SECONDS", 20)
	if err != nil {
		return Settings{}, err
	}
	maxRetries, err := positiveInt("HTTP_MAX_RETRIES", 3)
	if err != nil {
		return Settings{}, err
	}

	quotes := splitUniqueUpper(envOr("QUOTE_ASSETS", "USDT"))
	if len(quotes) == 0 {
		return Settings{}, fmt.Errorf("QUOTE_ASSETS 不能为空")
	}

	var threadID *int64
	if raw := strings.TrimSpace(os.Getenv("TELEGRAM_MESSAGE_THREAD_ID")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Settings{}, fmt.Errorf("TELEGRAM_MESSAGE_THREAD_ID 必须是整数")
		}
		threadID = &value
	}

	stateFile := envOr("STATE_FILE", "state/scheduler.json")
	if !filepath.IsAbs(stateFile) {
		stateFile = filepath.Join(projectDir, stateFile)
	}

	return Settings{
		BotToken:        os.Getenv("TELEGRAM_BOT_TOKEN"),
		ChatID:          os.Getenv("TELEGRAM_CHAT_ID"),
		MessageThreadID: threadID,
		TimezoneName:    timezoneName,
		Location:        location,
		ReportHours:     hours,
		GraceMinutes:    grace,
		QuoteAssets:     quotes,
		TopN:            topN,
		BinanceBaseURL:  strings.TrimRight(envOr("BINANCE_FAPI_BASE_URL", "https://fapi.binance.com"), "/"),
		Timeout:         time.Duration(timeoutSeconds) * time.Second,
		MaxRetries:      maxRetries,
		StateFile:       stateFile,
	}, nil
}

func (s Settings) ValidateTelegram() error {
	var missing []string
	if s.BotToken == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if s.ChatID == "" {
		missing = append(missing, "TELEGRAM_CHAT_ID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少 Telegram 配置：%s", strings.Join(missing, ", "))
	}
	return nil
}

func envOr(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func positiveInt(key string, fallback int) (int, error) {
	raw := envOr(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s 必须是大于 0 的整数，当前值：%q", key, raw)
	}
	return value, nil
}

func parseHours(raw string) ([]int, error) {
	seen := make(map[int]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 23 {
			return nil, fmt.Errorf("REPORT_HOURS 只能包含 0 到 23 的逗号分隔整数")
		}
		seen[value] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("REPORT_HOURS 不能为空")
	}
	result := make([]int, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Ints(result)
	return result, nil
}

func splitUniqueUpper(raw string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, part := range strings.Split(raw, ",") {
		value := strings.ToUpper(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
