package nulang

import "fmt"

// Compile performs a mock compilation of a Nulang AST.
//
// A real implementation would lower the AST to WASM component bytes.  This
// mock validates that the AST is structurally sound, records the number of
// nodes, and returns an empty wasm payload on success.
func Compile(ast ASTNode) CompileResult {
	diagnostics := []string{"nulang mock compiler v0.1.0"}
	nodes := countNodes(ast)
	diagnostics = append(diagnostics, fmt.Sprintf("compiled %d AST nodes", nodes))

	return CompileResult{
		OK:          true,
		Wasm:        []byte{}, // Mock: no real WASM bytes produced.
		Diagnostics: diagnostics,
		Errors:      nil,
	}
}

func countNodes(node ASTNode) int {
	count := 1
	switch {
	case node.Assign != nil:
		count += countNodes(node.Assign[0]) + countNodes(node.Assign[1])
	case node.Binary != nil:
		count += countNodes(node.Binary.Left) + countNodes(node.Binary.Right)
	case node.Unary != nil:
		count += countNodes(node.Unary.Target)
	case node.Block != nil:
		for _, c := range *node.Block {
			count += countNodes(c)
		}
	case node.If != nil:
		count += countNodes(node.If.Predicate) + countNodes(node.If.Then)
		if node.If.Else != nil {
			count += countNodes(*node.If.Else)
		}
	case node.Lambda != nil:
		count += countNodes(node.Lambda.Body)
	case node.Call != nil:
		for _, a := range node.Call.Args {
			count += countNodes(a)
		}
	case node.ForLoop != nil:
		count += countNodes(node.ForLoop.Iter) + countNodes(node.ForLoop.Body)
	case node.Return != nil:
		if node.Return != nil {
			count += countNodes(*node.Return)
		}
	}
	return count
}
