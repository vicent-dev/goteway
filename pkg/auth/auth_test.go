package auth

import (
	"context"
	"testing"
)

func TestIsValidToken(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want bool
	}{
			{
				name: "empty context",
				ctx:  context.Background(),
				want: false,
			},
		{
			name: "context with empty auth value",
			ctx:  context.WithValue(context.Background(), AUTH_CTX_KEY, ""),
			want: false,
		},
		{
			name: "context with non-empty auth value",
			ctx:  context.WithValue(context.Background(), AUTH_CTX_KEY, "Bearer token123"),
			want: true,
		},
		{
			name: "context with different non-empty value",
			ctx:  context.WithValue(context.Background(), AUTH_CTX_KEY, "abc"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidToken(tt.ctx)
			if got != tt.want {
				t.Errorf("IsValidToken() = %v, want %v", got, tt.want)
			}
		})
	}
}
