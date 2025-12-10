package trace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProvider(t *testing.T) {
	assert := require.New(t)

	provider, err := NewProvider(context.Background(), "http/protobuf", "test", "1.2.3")
	assert.NoError(err)

	assert.NotNil(provider)

	provider, err = NewProvider(context.Background(), "grpc", "test", "1.2.3")
	assert.NoError(err)

	assert.NotNil(provider)

	_, err = NewProvider(context.Background(), "", "test", "1.2.3")
	assert.NoError(err)
}
