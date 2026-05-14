package agent

import "testing"

// Reproducer for the f351afa1 WeChat session (2026-05-14 22:59): user asked
// "R1520 规格", agent recovered the data from the KB across 3 rounds, then
// the LLM (mimo-v2.5-pro) on round 2 emitted JUST this 88-rune Chinese
// "planning artifact" with finish_reason=stop:
//
//	"已获取完整的R1520数据手册和Wiki信息，现在为你整理规格摘要"
//
// Before the fix, isPlanningArtifact returned false (English-only prefixes
// + English-only action verbs), so the agent accepted the artifact as the
// final answer and ended the loop with answer_len=88. User saw the
// "got X, now I'll write Y" sentence and nothing else.
//
// After the fix, isPlanningArtifact returns true → engine.runReActIteration
// retries with a synthesis-nudge user message → LLM produces the actual
// spec summary.
func TestIsPlanningArtifact_DetectsChineseGotXNowYPattern(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			"f351afa1 reproducer · 已获取 X，现在为你整理 Y",
			"已获取完整的R1520数据手册和Wiki信息，现在为你整理规格摘要",
			true,
		},
		{
			"已查询 X，马上为您总结",
			"已查询到EG5120的相关产品资料，马上为您总结主要规格参数",
			true,
		},
		{
			"现在让我为你整理",
			"现在让我为你整理一下相关的产品信息",
			true,
		},
		{
			"我将为您准备",
			"我将为您准备一份详细的对比分析",
			true,
		},
		{
			"接下来为你梳理",
			"接下来为你梳理 LoRaWAN 产品线的主要差异点",
			true,
		},
		{
			"i'll fetch (existing English pattern preserved)",
			"I'll fetch the data from the knowledge base now.",
			true,
		},
		{
			"long real answer (must NOT be flagged)",
			"R1520LG 是 Robustel 推出的工业级 LoRaWAN 网关。" +
				"CPU 采用 i.MX 6ULL 792 MHz, 配 512 MB 内存和 8 GB Flash 存储。" +
				"接口方面提供 RS485 和以太网, 通信支持 LoRaWAN CN470/EU868/US915 频段。" +
				"防护等级 IP30, 工作温度 -40 到 +70 摄氏度, 支持壁挂或 DIN 导轨安装。" +
				"远程管理通过 RCMS 云平台实现, 支持 OTA 升级和地理空间仪表板。" +
				"软件方面预装 RobustOS, 兼容 Modbus RTU 和 BACnet 协议, 可与各种 BMS 系统集成。" +
				"典型应用场景包括智慧建筑能源管理、智慧农业环境监测、智慧城市路灯控制等。" +
				"竞品对比中, R1520LG 在成本上显著低于 Milesight UG65, 同时保留了完整的工业级特性。",
			false, // > 400 runes, real content
		},
		{
			"plain Q&A response (no planning prefix)",
			"R1520LG 支持 LoRaWAN class A/B/C 全协议。",
			false,
		},
		{
			"empty",
			"",
			false,
		},
		{
			// "已获取数据" → still planning-artifact-ish: agent said "got
			// data, done" without writing an answer. Retry-with-nudge
			// is the right behavior (prompt model to compose the answer
			// it just announced it had).
			"已获取数据 standalone — also retry-worthy",
			"已获取数据。完毕。",
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isPlanningArtifact(c.content)
			if got != c.want {
				t.Errorf("isPlanningArtifact(%q) = %v, want %v", c.content, got, c.want)
			}
		})
	}
}
