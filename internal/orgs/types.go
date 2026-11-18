package orgs

import "github.com/qlustered/qctl/internal/api"

// Type aliases for generated types
type (
	OrgItem          = api.OrganizationTinySchema
	OrgList          = api.OrganizationsListSchema
	OrgOrderBy       = api.OrganizationOrderBy
	PaginationSchema = api.PaginationSchema
)
