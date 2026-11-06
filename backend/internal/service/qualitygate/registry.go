package qualitygate

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/testgate"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// LoadRegistry 只在 daemon 启动时读取宿主登记策略；缺文件即禁用 Gate，HTTP 无添加命令的入口。
func LoadRegistry(path string) (map[string]domain.GatePolicy, error) {
	out := map[string]domain.GatePolicy{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, domain.ErrDeliveryBlocked
	}
	var policies []domain.GatePolicy
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policies); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, domain.ErrDeliveryBlocked
	}
	if len(policies) > 32 {
		return nil, domain.ErrDeliveryBlocked
	}
	for _, p := range policies {
		if err := testgate.ValidatePolicy(p); err != nil {
			return nil, err
		}
		if _, exists := out[p.ID]; exists {
			return nil, domain.ErrDeliveryBlocked
		}
		out[p.ID] = p
	}
	return out, nil
}
