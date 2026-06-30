// Package nulang implements a minimal Nulang AST and runtime.
//
// The types mirror the WIT interface defined in
// runtime/wasmcloud/wit/nulang.wit so the mock harness can compile and
// evaluate a small Nulang program without a full parser.
package nulang

// Value is a Nulang runtime value.
type Value struct {
	Nil      *struct{} `json:"nil,omitempty"`
	Boolean  *bool     `json:"boolean,omitempty"`
	Integer  *int64    `json:"integer,omitempty"`
	Float    *float64  `json:"float,omitempty"`
	String   *string   `json:"string,omitempty"`
	List     *[]Value  `json:"list,omitempty"`
	Map      *[]KVPair `json:"map,omitempty"`
}

// KVPair is a single key/value pair in a Nulang map.
type KVPair struct {
	Key   string `json:"key"`
	Value Value  `json:"value"`
}

// ASTNode is a Nulang AST node.
type ASTNode struct {
	Literal   *Value       `json:"literal,omitempty"`
	Identifier *string     `json:"identifier,omitempty"`
	Assign    *[2]ASTNode  `json:"assign,omitempty"`
	Call      *CallNode    `json:"call,omitempty"`
	Lambda    *LambdaNode  `json:"lambda,omitempty"`
	If        *IfNode       `json:"if,omitempty"`
	Block     *[]ASTNode    `json:"block,omitempty"`
	ForLoop   *ForLoopNode  `json:"for-loop,omitempty"`
	Return    *ASTNode      `json:"return,omitempty"`
	Binary    *BinaryNode   `json:"binary,omitempty"`
	Unary     *UnaryNode    `json:"unary,omitempty"`
}

// CallNode represents a function call.
type CallNode struct {
	Name string   `json:"name"`
	Args []ASTNode `json:"args"`
}

// LambdaNode represents a lambda expression.
type LambdaNode struct {
	Params []string `json:"params"`
	Body   ASTNode  `json:"body"`
}

// IfNode represents a conditional.
type IfNode struct {
	Predicate ASTNode  `json:"predicate"`
	Then      ASTNode  `json:"then"`
	Else      *ASTNode `json:"else,omitempty"`
}

// ForLoopNode represents a for-loop.
type ForLoopNode struct {
	Var    string  `json:"var"`
	Iter   ASTNode `json:"iter"`
	Body   ASTNode `json:"body"`
}

// BinaryNode represents a binary operation.
type BinaryNode struct {
	Op    BinaryOp `json:"op"`
	Left  ASTNode  `json:"left"`
	Right ASTNode  `json:"right"`
}

// UnaryNode represents a unary operation.
type UnaryNode struct {
	Op     UnaryOp `json:"op"`
	Target ASTNode `json:"target"`
}

// BinaryOp is a binary operator.
type BinaryOp string

const (
	Add                  BinaryOp = "add"
	Subtract             BinaryOp = "subtract"
	Multiply             BinaryOp = "multiply"
	Divide               BinaryOp = "divide"
	Modulo               BinaryOp = "modulo"
	Equal                BinaryOp = "equal"
	NotEqual             BinaryOp = "not-equal"
	LessThan             BinaryOp = "less-than"
	LessThanOrEqual      BinaryOp = "less-than-or-equal"
	GreaterThan          BinaryOp = "greater-than"
	GreaterThanOrEqual   BinaryOp = "greater-than-or-equal"
	And                  BinaryOp = "and"
	Or                   BinaryOp = "or"
)

// UnaryOp is a unary operator.
type UnaryOp string

const (
	Not     UnaryOp = "not"
	Negate  UnaryOp = "negate"
)

// Error represents a runtime error.
type Error struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Span    *Span   `json:"span,omitempty"`
}

// Span is a source location.
type Span struct {
	Line   uint32 `json:"line"`
	Column uint32 `json:"column"`
	Length uint32 `json:"length"`
}

// CompileResult is the result of a compile operation.
type CompileResult struct {
	OK          bool     `json:"ok"`
	Wasm        []byte   `json:"wasm,omitempty"`
	Diagnostics []string `json:"diagnostics"`
	Errors      []Error  `json:"errors"`
}

// EvalResult is the result of an evaluation.
type EvalResult struct {
	OK     bool     `json:"ok"`
	Value  *Value   `json:"value,omitempty"`
	Errors []Error  `json:"errors"`
}

// IsNil reports whether v is the nil value.
func (v Value) IsNil() bool {
	return v.Nil != nil
}
