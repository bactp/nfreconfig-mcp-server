package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"nfreconfig-mcp-server/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	httpAddr = flag.String("http", "", "if set, use streamable HTTP to serve MCP (on this address), instead of stdin/stdout")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "nfreconfig-mcp-server",
		Version: "0.1.0",
	}, nil)

	tools.AddToolsToServer(server)

	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
			return server
		}, nil)

		fmt.Fprintf(os.Stderr, "MCP server listening at %s\n", *httpAddr)
		return http.ListenAndServe(*httpAddr, handler)
	} else {
		// fmt.Fprintf(os.Stderr, "Starting MCP server on stdio\n")
		return server.Run(context.Background(), mcp.NewStdioTransport())
	}
}
