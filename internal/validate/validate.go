package validate

import "github.com/akijowski/pipefitter/internal/pipeline"

type Finding struct {
	Rule    string
	Message string
}

type Rule interface {
	Name() string
	Check(doc pipeline.Document) []Finding
}

func Rules() []Rule {
	return []Rule{
		DependsOn{},
	}
}

func Run(doc pipeline.Document, rules []Rule) []Finding {
	var f []Finding
	for _, rule := range rules {
		f = append(f, rule.Check(doc)...)
	}

	return f
}
