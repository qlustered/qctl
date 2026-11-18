package deliverables

import (
	"fmt"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

func decodeBinding[T any](body []byte) (*T, error) {
	var result T
	if err := jsonnum.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode deliverable binding response: %w", err)
	}
	return &result, nil
}
