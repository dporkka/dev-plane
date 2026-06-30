# Nulang Mock Harness

A buildable Go stub implementing a minimal Nulang AST, evaluator, and mock
compiler.  It mirrors the WIT interfaces declared in
`runtime/wasmcloud/wit/nulang.wit`.

## Build

```bash
cd runtime/wasmcloud/components/nulang-mock-harness
go build ./...
```

## Run

```bash
go run .
```

The harness evaluates a hard-coded AST expression and prints the compile and
eval results as JSON.

## wasmCloud Component Build

To deploy as a WASI Preview 2 component, wrap this logic with a WIT world and
build with one of:

- `cargo component build` (Rust guest)
- `tinygo build -target=wasi` plus `wit-bindgen-go`

The `runtime/wasmcloud/wit/nulang.wit` world `nulang-runtime` exports the
`compile`, `eval`, and `parse` interfaces expected by the lattice.
