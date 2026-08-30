package report

import (
	"fmt"
	stdhtml "html"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"binance-monitor/internal/catalog"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/marketquery"
	"binance-monitor/internal/model"
	"github.com/shopspring/decimal"
)

const maxTelegramUTF16Units = 3900

var (
	reportAnchorPattern = regexp.MustCompile(`<a href="([^"]+)">([^<]+)</a>`)
	reportTagPattern    = regexp.MustCompile(`</?(?:b|pre)>`)
)

func Render(snapshot Snapshot, assets *catalog.Catalog, location *time.Location, topN int) ([]string, error) {
	if snapshot.AsOf.IsZero() || len(snapshot.Groups) != len(market.ReturnHorizons())*2 || assets == nil || location == nil || topN <= 0 {
		return nil, fmt.Errorf("V2 report snapshot 或配置无效")
	}
	localTime := snapshot.AsOf.In(location)
	blocks := []string{
		fmt.Sprintf("<b>Binance USDⓈ-M 多周期涨幅 Top %d</b>", topN),
		fmt.Sprintf("数据时间：<b>%s（%s）</b>", localTime.Format("2006-01-02 15:04:05"), stdhtml.EscapeString(location.String())),
		fmt.Sprintf(
			"数据覆盖：%d/%d 个收益指标有效（%s%%）；当前正收益候选项 %d 个。",
			snapshot.Quality.ValidMetrics,
			snapshot.Quality.ActiveMetrics,
			snapshot.Quality.CoveragePercent.StringFixed(3),
			positiveMetrics(snapshot.Groups),
		),
		"口径：Crypto 与 TradFi 分板块，分别按 15m、1h、4h、24h 自计算收益率排名；仅展示正收益且通过质量门禁的标的。",
	}
	for _, group := range snapshot.Groups {
		blocks = append(blocks, rankingBlock(group, topN))
	}
	messages, err := packBlocks(blocks)
	if err != nil {
		return nil, err
	}

	introductions := []string{"<b>上榜标的简介</b>"}
	seen := make(map[string]struct{})
	for _, group := range snapshot.Groups {
		for _, item := range group.Items {
			if _, exists := seen[item.Symbol]; exists {
				continue
			}
			seen[item.Symbol] = struct{}{}
			introductions = append(introductions, introBlock(item, assets))
		}
	}
	introMessages, err := packBlocks(introductions)
	if err != nil {
		return nil, err
	}
	messages = append(messages, introMessages...)
	warning := "注：本报告只描述已发生的行情相对强弱，不是买入建议。永续合约包含杠杆、资金费率、流动性和强平风险。"
	if telegramUTF16Length(warning) > maxTelegramUTF16Units {
		return nil, fmt.Errorf("风险提示超过 Telegram 长度限制")
	}
	messages = append(messages, warning)
	return messages, nil
}

func rankingBlock(group marketquery.Ranking, topN int) string {
	sectorLabel := "Crypto"
	if group.Sector == market.SectorTradFi {
		sectorLabel = "TradFi"
	}
	var builder strings.Builder
	fmt.Fprintf(
		&builder,
		"<b>%s %s 涨幅前 %d｜有效 %d/%d｜上涨 %d</b>\n<pre>\n",
		sectorLabel, group.Horizon, topN, group.EligibleCount, group.ActiveCount, group.PositiveCount,
	)
	if len(group.Items) == 0 {
		builder.WriteString("本时段无符合条件标的\n")
	} else {
		for _, item := range group.Items {
			fmt.Fprintf(&builder, "%d. %s  %s%%\n", item.Rank, item.Symbol, signed(item.ReturnPercent, 3))
			fmt.Fprintf(
				&builder,
				"   15m %s | 1h %s | 4h %s | 24h %s\n",
				metricText(item.Returns[market.ReturnHorizon15m]),
				metricText(item.Returns[market.ReturnHorizon1h]),
				metricText(item.Returns[market.ReturnHorizon4h]),
				metricText(item.Returns[market.ReturnHorizon24h]),
			)
			fmt.Fprintf(
				&builder,
				"   最新 %s | 24h额 %s\n",
				formatDecimal(item.CurrentPrice), formatVolume(item.QuoteVolume24h),
			)
		}
	}
	builder.WriteString("</pre>")
	return builder.String()
}

func metricText(metric marketquery.ReturnMetric) string {
	if !metric.IsValid || metric.ReturnPercent == nil {
		return "N/A"
	}
	return signed(*metric.ReturnPercent, 3) + "%"
}

func signed(value decimal.Decimal, places int32) string {
	text := value.Round(places).StringFixed(places)
	if !value.IsNegative() {
		return "+" + text
	}
	return text
}

func formatDecimal(value decimal.Decimal) string {
	if value.GreaterThanOrEqual(decimal.NewFromInt(1000)) {
		return withThousands(value.Round(2).StringFixed(2))
	}
	return value.String()
}

func formatVolume(value decimal.Decimal) string {
	units := []struct {
		threshold decimal.Decimal
		label     string
	}{
		{decimal.NewFromInt(1_000_000_000), "B"},
		{decimal.NewFromInt(1_000_000), "M"},
		{decimal.NewFromInt(1_000), "K"},
	}
	for _, unit := range units {
		if value.GreaterThanOrEqual(unit.threshold) {
			return value.Div(unit.threshold).Round(2).StringFixed(2) + unit.label
		}
	}
	return value.Round(2).StringFixed(2)
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

func introBlock(item marketquery.RankingItem, assets *catalog.Catalog) string {
	board := model.BoardCrypto
	if item.Sector == market.SectorTradFi {
		board = model.BoardTradFi
	}
	intro := assets.Describe(model.Contract{
		Symbol: item.Symbol, BaseAsset: item.BaseAsset, Board: board,
	})
	label := item.BaseAsset
	if intro.Name != "" && intro.Name != item.BaseAsset {
		label += "（" + intro.Name + "）"
	}
	source := ""
	if intro.URL != "" {
		source = fmt.Sprintf(` <a href="%s">资料来源</a>`, stdhtml.EscapeString(intro.URL))
	}
	return fmt.Sprintf(
		"<b>%s / %s</b>：%s%s",
		stdhtml.EscapeString(label), stdhtml.EscapeString(item.Symbol),
		stdhtml.EscapeString(intro.Description), source,
	)
}

func positiveMetrics(groups []marketquery.Ranking) int {
	total := 0
	for _, group := range groups {
		total += group.PositiveCount
	}
	return total
}

func packBlocks(blocks []string) ([]string, error) {
	messages := make([]string, 0)
	current := ""
	for _, block := range blocks {
		if telegramUTF16Length(block) > maxTelegramUTF16Units {
			return nil, fmt.Errorf("单个 Telegram 报告块超过 %d UTF-16 units", maxTelegramUTF16Units)
		}
		candidate := block
		if current != "" {
			candidate = current + "\n\n" + block
		}
		if current != "" && telegramUTF16Length(candidate) > maxTelegramUTF16Units {
			messages = append(messages, current)
			current = block
		} else {
			current = candidate
		}
	}
	if current != "" {
		messages = append(messages, current)
	}
	return messages, nil
}

func telegramUTF16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func Plain(messages []string) string {
	text := strings.Join(messages, "\n\n")
	text = reportAnchorPattern.ReplaceAllString(text, "$2（$1）")
	text = reportTagPattern.ReplaceAllString(text, "")
	return stdhtml.UnescapeString(text)
}
