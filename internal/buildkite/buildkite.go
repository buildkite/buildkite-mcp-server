package buildkite

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
)

// requiredParam validates and retrieves a required parameter of a specified type from a CallToolRequest.
// It checks for the parameter's presence, type correctness, and non-emptiness.
// Returns the parameter value if valid, or an error describing the validation failure.
func requiredParam[T comparable](request mcp.CallToolRequest, name string) (T, error) {
	var empty T

	if request.Params.Arguments[name] == nil {
		return empty, errors.New("missing required argument: " + name)
	}

	val, ok := request.Params.Arguments[name].(T)
	if !ok {
		return empty, errors.New("invalid type for argument: " + name)
	}

	if val == empty {
		return empty, errors.New("argument cannot be empty: " + name)
	}

	return val, nil
}

func optionalPaginationParams(r mcp.CallToolRequest) (buildkite.ListOptions, error) {
	page, err := optionalIntParamWithDefault(r, "page", 1)
	if err != nil {
		return buildkite.ListOptions{}, err
	}
	perPage, err := optionalIntParamWithDefault(r, "perPage", 1)
	if err != nil {
		return buildkite.ListOptions{}, err
	}
	return buildkite.ListOptions{
		Page:    page,
		PerPage: perPage,
	}, nil
}

func optionalIntParamWithDefault(request mcp.CallToolRequest, name string, defaultValue int) (int, error) {
	if request.Params.Arguments[name] == nil {
		return defaultValue, nil
	}

	switch request.Params.Arguments[name].(type) {
	case int:
		// check if the value is an int
		return request.Params.Arguments[name].(int), nil
	case float64:
		return int(request.Params.Arguments[name].(float64)), nil
	case string:
		// check if the value is a string
		// convert to int
		val, err := strconv.Atoi(request.Params.Arguments[name].(string))
		if err != nil {
			return defaultValue, errors.New("invalid value for argument: " + name + ", must be an integer")
		}
		return val, nil
	default:
		return defaultValue, fmt.Errorf("invalid type: %T for argument: %s", request.Params.Arguments[name], name)
	}
}

func withPagination() mcp.ToolOption {
	return func(tool *mcp.Tool) {
		mcp.WithNumber("page",
			mcp.Description("Page number for pagination (min 1)"),
			mcp.Min(1),
		)(tool)

		mcp.WithNumber("perPage",
			mcp.Description("Results per page for pagination (min 1, max 100)"),
			mcp.Min(1),
			mcp.Max(100),
		)(tool)
	}
}
