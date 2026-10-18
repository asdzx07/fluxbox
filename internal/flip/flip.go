// internal/flip/flip.go —— 复刻实机报告中的 50 字节规则集热切换开关
package flip

import (
	"os"
	"path/filepath"
)

const (
	ruleMatchAll  = `{"version":3,"rules":[{"network":["tcp","udp"]}]}`
	ruleMatchNone = `{"version":3,"rules":[{"domain":["obflip-off.invalid"]}]}`
)

// WritePolicyFlip 切换分流策略开关：改写 50B 小文件触发 sing-box 原生热重载，0 断流！
func WritePolicyFlip(flipDir, policyID string, enabled bool) error {
	if err := os.MkdirAll(flipDir, 0755); err != nil {
		return err
	}
	targetFile := filepath.Join(flipDir, "obflip-"+policyID+".json")
	tmpFile := targetFile + ".tmp"

	content := ruleMatchNone
	if enabled {
		content = ruleMatchAll
	}
	// 原子写 + Rename，彻底规避实机报告第 6.3 节提到的 ENOENT 瞬时报错！
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, targetFile)
}