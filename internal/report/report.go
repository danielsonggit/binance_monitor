package report

import (
	"fmt"
	stdhtml "html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"binance-monitor/internal/catalog"
	"binance-monitor/internal/model"
)

const (
	tickerURL       = "https://fapi.binance.com/fapi/v1/ticker/24hr"
	exchangeURL     = "https://fapi.binance.com/fapi/v1/exchangeInfo"
	maxMessageRunes = 3900
)

var (
	anchorPattern = regexp.MustCompile(`<a href="([^"]+)">([^<]+)</a>`)
	tagPattern    = regexp.MustCompile(`</?(?:b|pre)>`)
)

type Options struct {
	TopN          int
	QuoteLabel    string
	TimezoneLabel string
}

func TelegramMessages(
	rankings model.Rankings,
	generatedAt time.Time,
	assets *catalog.Catalog,
	options Options,
) []string {
	if options.TopN <= 0 {
		options.TopN = 5
	}
	if options.QuoteLabel == "" {
		options.QuoteLabel = "USDT"
	}
	if options.TimezoneLabel == "" {
		options.TimezoneLabel = "北京时间"
	}

	summary := []string{
		"<b>Binance USDⓈ-M 24h 涨幅榜</b>",
		fmt.Sprintf(
			"数据时间：<b>%s（%s）</b>",
			stdhtml.EscapeString(generatedAt.Format("2006-01-02 15:04:05")),
			stdhtml.EscapeString(options.TimezoneLabel),
		),
		fmt.Sprintf(
			"口径：在 Binance 当前处于 TRADING 状态的 %s 永续合约中，按最新成交价的滚动 24 小时涨幅排名，仅展示上涨标的。",
			stdhtml.EscapeString(options.QuoteLabel),
		),
		rankingBlock(
			fmt.Sprintf("TradFi 涨幅前 %d｜标的 / 合约 / 最新价 / 24h", options.TopN),
			rankings.TradFiGainers,
		),
		rankingBlock(
			fmt.Sprintf("Crypto 涨幅前 %d｜代币 / 合约 / 最新价 / 24h", options.TopN),
			rankings.CryptoGainers,
		),
		fmt.Sprintf(
			`<a href="%s">行情接口</a> · <a href="%s">合约分类接口</a>`,
			tickerURL,
			exchangeURL,
		),
	}

	tradFi := uniqueMovers(rankings.TradFiGainers)
	crypto := uniqueMovers(rankings.CryptoGainers)
	messages := packBlocks(summary)
	messages = append(messages, packBlocks(introBlocks("上榜 TradFi 标的简介", tradFi, assets))...)
	messages = append(messages, packBlocks(introBlocks("上榜 Crypto 标的简介", crypto, assets))...)
	messages = append(
		messages,
		"注：杠杆 ETF 会每日重置，长期收益可能因复利和路径依赖偏离名义倍数；"+
			"Binance 永续合约还叠加资金费率、合约杠杆和强平风险。"+
			"本报告仅为行情排名，不构成买卖建议。",
	)
	return messages
}

func Plain(messages []string) string {
	text := strings.Join(messages, "\n\n")
	text = anchorPattern.ReplaceAllString(text, "$2（$1）")
	text = tagPattern.ReplaceAllString(text, "")
	return stdhtml.UnescapeString(text)
}

func rankingBlock(title string, rows []model.Mover) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "<b>%s</b>\n<pre>\n", stdhtml.EscapeString(title))
	if len(rows) == 0 {
		builder.WriteString("暂无符合条件的标的\n")
	} else {
		for index, mover := range rows {
			fmt.Fprintf(
				&builder,
				"%d. %-10s %-16s %12s %10s\n",
				index+1,
				mover.Contract.BaseAsset,
				mover.Contract.Symbol,
				formatPrice(mover.Ticker.LastPrice),
				fmt.Sprintf("%+.3f%%", mover.Ticker.ChangePercent),
			)
		}
	}
	builder.WriteString("</pre>")
	return builder.String()
}

func formatPrice(value float64) string {
	if value >= 1000 {
		return withThousands(fmt.Sprintf("%.2f", value))
	}
	rendered := strconv.FormatFloat(value, 'f', -1, 64)
	if value >= 1 && !strings.Contains(rendered, ".") {
		rendered += ".00"
	}
	return rendered
}

func withThousands(value string) string {
	integer, fraction, found := strings.Cut(value, ".")
	var builder strings.Builder
	for index, char := range integer {
		if index > 0 && (len(integer)-index)%3 == 0 {
			builder.WriteByte(',')
		}
		builder.WriteRune(char)
	}
	if found {
		builder.WriteByte('.')
		builder.WriteString(fraction)
	}
	return builder.String()
}

func uniqueMovers(groups ...[]model.Mover) []model.Mover {
	seen := make(map[string]struct{})
	var result []model.Mover
	for _, group := range groups {
		for _, mover := range group {
			if _, exists := seen[mover.Contract.BaseAsset]; exists {
				continue
			}
			seen[mover.Contract.BaseAsset] = struct{}{}
			result = append(result, mover)
		}
	}
	return result
}

func introBlocks(title string, movers []model.Mover, assets *catalog.Catalog) []string {
	blocks := []string{"<b>" + stdhtml.EscapeString(title) + "</b>"}
	for _, mover := range movers {
		intro := assets.Describe(mover.Contract)
		symbol := stdhtml.EscapeString(mover.Contract.BaseAsset)
		name := stdhtml.EscapeString(intro.Name)
		label := symbol
		if name != symbol {
			label += "（" + name + "）"
		}
		source := ""
		if intro.URL != "" {
			source = fmt.Sprintf(
				` <a href="%s">资料来源</a>`,
				stdhtml.EscapeString(intro.URL),
			)
		}
		blocks = append(
			blocks,
			fmt.Sprintf(
				"<b>%s</b>：%s%s",
				label,
				stdhtml.EscapeString(intro.Description),
				source,
			),
		)
	}
	return blocks
}

func packBlocks(blocks []string) []string {
	var messages []string
	current := ""
	for _, block := range blocks {
		candidate := block
		if current != "" {
			candidate = current + "\n\n" + block
		}
		if current != "" && utf8.RuneCountInString(candidate) > maxMessageRunes {
			messages = append(messages, current)
			current = block
		} else {
			current = candidate
		}
	}
	if current != "" {
		messages = append(messages, current)
	}
	return messages
}
