module github.com/blockberries/leaderberry

go 1.25.6

require github.com/blockberries/cramberry v1.5.3

replace (
	github.com/blockberries/blockberry => ../blockberry
	github.com/blockberries/cramberry => ../cramberry
	github.com/blockberries/glueberry => ../glueberry
	github.com/blockberries/looseberry => ../looseberry
)
