package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"binance-monitor/internal/model"
)

//go:embed assets.json
var embeddedAssets []byte

type Intro struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

type Catalog struct {
	entries map[string]Intro
}

func Default() (*Catalog, error) {
	return FromBytes(embeddedAssets)
}

func FromFile(path string) (*Catalog, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取资产简介资料库 %s: %w", path, err)
	}
	return FromBytes(content)
}

func FromBytes(content []byte) (*Catalog, error) {
	var raw map[string]Intro
	if err := json.Unmarshal(content, &raw); err != nil {
		return nil, fmt.Errorf("解析资产简介资料库: %w", err)
	}
	entries := make(map[string]Intro, len(raw))
	for symbol, intro := range raw {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		intro.Name = strings.TrimSpace(intro.Name)
		intro.Description = strings.TrimSpace(intro.Description)
		intro.URL = strings.TrimSpace(intro.URL)
		if symbol == "" || intro.Description == "" {
			continue
		}
		if intro.Name == "" {
			intro.Name = symbol
		}
		entries[symbol] = intro
	}
	return &Catalog{entries: entries}, nil
}

func (c *Catalog) Describe(contract model.Contract) Intro {
	if intro, exists := c.entries[contract.BaseAsset]; exists {
		return intro
	}

	tags := strings.Join(contract.UnderlyingSubTypes, "、")
	var description string
	if contract.Board == model.BoardTradFi {
		kind := map[string]string{
			"COMMODITY": "传统商品",
			"EQUITY":    "股票或股票相关产品",
			"ETF":       "交易所交易基金",
			"INDEX":     "传统市场指数",
			"PREMARKET": "Pre-IPO 参考资产",
		}[contract.UnderlyingType]
		if kind == "" {
			kind = "传统金融资产"
		}
		detail := ""
		if tags != "" {
			detail = "；Binance 分类标签为 " + tags
		}
		description = fmt.Sprintf(
			"%s 是 Binance USDⓈ-M TradFi 永续合约所跟踪的%s%s。该合约不等于持有对应现货资产。",
			contract.BaseAsset,
			kind,
			detail,
		)
	} else {
		detail := ""
		if tags != "" {
			detail = "；Binance 板块标签为 " + tags
		}
		description = fmt.Sprintf(
			"%s 是 %s USDⓈ-M 永续合约的基础资产%s。本地资料库尚无经过核验的项目简介。",
			contract.BaseAsset,
			contract.Symbol,
			detail,
		)
	}
	return Intro{Name: contract.BaseAsset, Description: description}
}
