// The authority audit is a separate Go module on purpose: it keeps the operator
// module's go.mod, go.sum and .golangci.yml untouched, which are the three files
// most likely to conflict on an upstream merge (specification section 20.2).
module github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/audit

go 1.26.5

require (
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/tools v0.47.0
)

require (
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
)
