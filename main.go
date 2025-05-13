package main

import (
	"context"
	"fmt"

	"github.com/cli/go-gh/v2"
	"github.com/google/uuid"
)

func main() {
	ctx := context.Background()
	listener, port, err := startServer()
	if err != nil {
		fmt.Println("Error starting server:", err)
		return
	}
	defer listener.Close()
	fmt.Printf("Server started on port %d\n", port)
	socketId := uuid.New()

	socketPath := "/tmp/ado-auth-" + socketId.String() + ".sock"
	gh.ExecInteractive(ctx, "cs", "ssh", "--", "-R", fmt.Sprintf("%s:localhost:%d", socketPath, port), "-t")
}

// For more examples of using go-gh, see:
// https://github.com/cli/go-gh/blob/trunk/example_gh_test.go
