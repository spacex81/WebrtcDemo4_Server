# Use the official Golang image to create a build artifact.
FROM golang:1.22 as builder

# Set the working directory outside $GOPATH to enable the support for modules.
WORKDIR /app

# Copy go mod and sum files
COPY server/go.mod server/go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code into the container
COPY server/ .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o sfu-server .

# Use a Docker multi-stage build to create a lean production image.
FROM alpine:latest  
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/sfu-server .

# Command to run the executable
CMD ["./sfu-server"]
