package dataset_rules //nolint:revive // Public package name matches the established resource domain.

// GetDatasetRulesByState filters every matching page before applying pagination.
func (c *Client) GetDatasetRulesByState(
	accessToken string, datasetID int, params GetDatasetRulesParams, state DatasetRuleState,
) (*DatasetRuleList, error) {
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.Limit <= 0 {
		params.Limit = 100
	}
	allParams := params
	allParams.Limit = 1000
	rules, err := c.getAllDatasetRules(accessToken, datasetID, allParams)
	if err != nil {
		return nil, err
	}
	filtered := make([]DatasetRuleTiny, 0)
	for _, rule := range rules {
		if rule.State == state {
			filtered = append(filtered, rule)
		}
	}
	total := len(filtered)
	start := total
	if params.Page-1 <= total/params.Limit {
		start = min((params.Page-1)*params.Limit, total)
	}
	end := start + min(params.Limit, total-start)
	return &DatasetRuleList{Results: filtered[start:end], TotalRows: &total, Page: &params.Page}, nil
}
