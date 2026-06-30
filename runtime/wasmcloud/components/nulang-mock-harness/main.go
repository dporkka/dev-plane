// Nulang mock harness -- a buildable Go stub for the Nulang runtime.
//
// This program demonstrates compiling and evaluating a minimal Nulang AST.
// For wasmCloud deployment the same logic should be packaged as a WASI P2
// component (e.g. via cargo-component or TinyGo) and wired through the WIT
// interfaces in runtime/wasmcloud/wit/nulang.wit.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"dev-plane/runtime/wasmcloud/components/nulang-mock-harness/nulang"
)

func main() {
	// Build a small AST: ((10 + 5) * 2) == 30
	expr := nulang.ASTNode{
		Binary: &nulang.BinaryNode{
			Op: nulang.Equal,
			Left: nulang.ASTNode{
				Binary: &nulang.BinaryNode{
					Op: nulang.Multiply,
					Left: nulang.ASTNode{
						Binary: &nulang.BinaryNode{
							Op:    nulang.Add,
							Left:  intLiteral(10),
							Right: intLiteral(5),
						},
					},
					Right: intLiteral(2),
				},
			},
			Right: intLiteral(30),
		},
	}

	fmt.Println("=== Nulang Mock Harness ===")

	compileResult := nulang.Compile(expr)
	printJSON("compile result", compileResult)

	evalResult := nulang.Eval(expr)
	printJSON("eval result", evalResult)

	if evalResult.OK && evalResult.Value != nil && evalResult.Value.Boolean != nil && *evalResult.Value.Boolean {
		fmt.Println("OK: mock harness produced expected true result")
		os.Exit(0)
	}
	fmt.Println("FAIL: mock harness did not produce expected result")
	os.Exit(1)
}

func intLiteral(n int64) nulang.ASTNode {
	return nulang.ASTNode{Literal: &nulang.Value{Integer: &n}}
}

func printJSON(label string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s marshal error: %v\n", label, err)
		return
	}
	fmt.Printf("%s:\n%s\n\n", label, b)
}
