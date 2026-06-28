// Package crypto provides shared cryptographic primitives for the AI Dev Control Plane.
//
// It exposes an AES-256-GCM keyring suitable for encrypting integration tokens
// and other small secrets with caller-supplied additional authenticated data.
module github.com/ai-dev-control-plane/crypto

go 1.25.11
