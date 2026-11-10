package core

import (
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/lexer"
)

// MaxGraphQLSyntaxDepth bounds recursive parser work independently of tokens.
const MaxGraphQLSyntaxDepth = 128

func checkGraphQLNesting(source string) error {
	l := lexer.New(&ast.Source{Input: source})
	depth := 0
	for {
		token, err := l.ReadToken()
		if err != nil {
			return graphQLParseError(err)
		}
		switch token.Kind {
		case lexer.EOF:
			return nil
		case lexer.ParenL, lexer.BracketL, lexer.BraceL:
			depth++
			if depth > MaxGraphQLSyntaxDepth {
				return configErrorf("graphql syntax nesting exceeds %d", MaxGraphQLSyntaxDepth)
			}
		case lexer.ParenR, lexer.BracketR, lexer.BraceR:
			depth--
		}
	}
}
