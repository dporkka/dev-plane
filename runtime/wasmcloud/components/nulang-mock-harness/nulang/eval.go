package nulang

import (
	"fmt"
)

// Env holds variable bindings.
type Env struct {
	vars   map[string]Value
	parent *Env
}

// NewEnv creates a new environment.
func NewEnv() *Env {
	return &Env{vars: make(map[string]Value)}
}

// NewChild creates a child environment.
func (e *Env) NewChild() *Env {
	return &Env{vars: make(map[string]Value), parent: e}
}

// Get looks up a variable.
func (e *Env) Get(name string) (Value, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
	}
	return Value{}, false
}

// Set assigns a variable.
func (e *Env) Set(name string, v Value) {
	e.vars[name] = v
}

// Eval evaluates a node in a fresh environment.
func Eval(node ASTNode) EvalResult {
	return EvalWithBindings(node, nil)
}

// EvalWithBindings evaluates a node with initial bindings.
func EvalWithBindings(node ASTNode, bindings map[string]Value) EvalResult {
	env := NewEnv()
	for name, v := range bindings {
		env.Set(name, v)
	}
	v, err := evalNode(env, node)
	if err != nil {
		return EvalResult{OK: false, Errors: []Error{*err}}
	}
	return EvalResult{OK: true, Value: &v}
}

func evalNode(env *Env, node ASTNode) (Value, *Error) {
	switch {
	case node.Literal != nil:
		return *node.Literal, nil

	case node.Identifier != nil:
		v, ok := env.Get(*node.Identifier)
		if !ok {
			return Value{}, &Error{Code: "unbound-identifier", Message: fmt.Sprintf("unbound identifier: %s", *node.Identifier)}
		}
		return v, nil

	case node.Assign != nil:
		name := ""
		if node.Assign[0].Identifier != nil {
			name = *node.Assign[0].Identifier
		} else {
			return Value{}, &Error{Code: "invalid-assign", Message: "assignment target must be an identifier"}
		}
		v, err := evalNode(env, node.Assign[1])
		if err != nil {
			return Value{}, err
		}
		env.Set(name, v)
		return v, nil

	case node.Binary != nil:
		return evalBinary(env, node.Binary)

	case node.Unary != nil:
		return evalUnary(env, node.Unary)

	case node.Block != nil:
		var last Value
		for _, child := range *node.Block {
			v, err := evalNode(env, child)
			if err != nil {
				return Value{}, err
			}
			last = v
		}
		return last, nil

	case node.If != nil:
		pred, err := evalNode(env, node.If.Predicate)
		if err != nil {
			return Value{}, err
		}
		truthy := isTruthy(pred)
		if truthy {
			return evalNode(env, node.If.Then)
		}
		if node.If.Else != nil {
			return evalNode(env, *node.If.Else)
		}
		return Value{Nil: &struct{}{}}, nil

	case node.Call != nil:
		return evalCall(env, node.Call)

	case node.Lambda != nil:
		return Value{Nil: &struct{}{}}, nil

	case node.ForLoop != nil:
		return evalForLoop(env, node.ForLoop)

	case node.Return != nil:
		if node.Return == nil {
			return Value{Nil: &struct{}{}}, nil
		}
		return evalNode(env, *node.Return)

	default:
		return Value{}, &Error{Code: "unsupported-node", Message: "unsupported AST node"}
	}
}

func evalBinary(env *Env, b *BinaryNode) (Value, *Error) {
	left, err := evalNode(env, b.Left)
	if err != nil {
		return Value{}, err
	}
	right, err := evalNode(env, b.Right)
	if err != nil {
		return Value{}, err
	}

	// Numeric ops.
	if left.Integer != nil && right.Integer != nil {
		l, r := *left.Integer, *right.Integer
		switch b.Op {
		case Add:
			res := l + r
			return Value{Integer: &res}, nil
		case Subtract:
			res := l - r
			return Value{Integer: &res}, nil
		case Multiply:
			res := l * r
			return Value{Integer: &res}, nil
		case Divide:
			if r == 0 {
				return Value{}, &Error{Code: "divide-by-zero", Message: "division by zero"}
			}
			res := l / r
			return Value{Integer: &res}, nil
		case Modulo:
			if r == 0 {
				return Value{}, &Error{Code: "divide-by-zero", Message: "modulo by zero"}
			}
			res := l % r
			return Value{Integer: &res}, nil
		case Equal:
			res := l == r
			return Value{Boolean: &res}, nil
		case NotEqual:
			res := l != r
			return Value{Boolean: &res}, nil
		case LessThan:
			res := l < r
			return Value{Boolean: &res}, nil
		case LessThanOrEqual:
			res := l <= r
			return Value{Boolean: &res}, nil
		case GreaterThan:
			res := l > r
			return Value{Boolean: &res}, nil
		case GreaterThanOrEqual:
			res := l >= r
			return Value{Boolean: &res}, nil
		}
	}

	// String concat / compare.
	if left.String != nil && right.String != nil {
		l, r := *left.String, *right.String
		switch b.Op {
		case Add:
			res := l + r
			return Value{String: &res}, nil
		case Equal:
			res := l == r
			return Value{Boolean: &res}, nil
		case NotEqual:
			res := l != r
			return Value{Boolean: &res}, nil
		}
	}

	// Boolean ops.
	if left.Boolean != nil && right.Boolean != nil {
		l, r := *left.Boolean, *right.Boolean
		switch b.Op {
		case Equal:
			res := l == r
			return Value{Boolean: &res}, nil
		case NotEqual:
			res := l != r
			return Value{Boolean: &res}, nil
		case And:
			res := l && r
			return Value{Boolean: &res}, nil
		case Or:
			res := l || r
			return Value{Boolean: &res}, nil
		}
	}

	return Value{}, &Error{Code: "type-mismatch", Message: fmt.Sprintf("operator %s not supported for operands", b.Op)}
}

func evalUnary(env *Env, u *UnaryNode) (Value, *Error) {
	v, err := evalNode(env, u.Target)
	if err != nil {
		return Value{}, err
	}
	switch u.Op {
	case Not:
		if v.Boolean == nil {
			return Value{}, &Error{Code: "type-mismatch", Message: "not requires boolean"}
		}
		res := !*v.Boolean
		return Value{Boolean: &res}, nil
	case Negate:
		if v.Integer == nil {
			return Value{}, &Error{Code: "type-mismatch", Message: "negate requires integer"}
		}
		res := -*v.Integer
		return Value{Integer: &res}, nil
	}
	return Value{}, &Error{Code: "unsupported-op", Message: fmt.Sprintf("unsupported unary operator: %s", u.Op)}
}

func evalCall(env *Env, c *CallNode) (Value, *Error) {
	// Built-ins.
	switch c.Name {
	case "len":
		if len(c.Args) != 1 {
			return Value{}, &Error{Code: "arity", Message: "len expects 1 argument"}
		}
		arg, err := evalNode(env, c.Args[0])
		if err != nil {
			return Value{}, err
		}
		var n int64
		if arg.List != nil {
			n = int64(len(*arg.List))
		} else if arg.String != nil {
			n = int64(len(*arg.String))
		} else {
			return Value{}, &Error{Code: "type-mismatch", Message: "len requires list or string"}
		}
		return Value{Integer: &n}, nil
	default:
		return Value{}, &Error{Code: "unknown-function", Message: fmt.Sprintf("unknown function: %s", c.Name)}
	}
}

func evalForLoop(env *Env, f *ForLoopNode) (Value, *Error) {
	iter, err := evalNode(env, f.Iter)
	if err != nil {
		return Value{}, err
	}
	if iter.List == nil {
		return Value{}, &Error{Code: "type-mismatch", Message: "for-loop requires list iterator"}
	}
	child := env.NewChild()
	var last Value
	for _, item := range *iter.List {
		child.Set(f.Var, item)
		v, err := evalNode(child, f.Body)
		if err != nil {
			return Value{}, err
		}
		last = v
	}
	return last, nil
}

func isTruthy(v Value) bool {
	if v.Boolean != nil {
		return *v.Boolean
	}
	if v.Integer != nil {
		return *v.Integer != 0
	}
	if v.String != nil {
		return *v.String != ""
	}
	if v.Nil != nil {
		return false
	}
	return true
}
