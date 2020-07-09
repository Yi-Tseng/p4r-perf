// Copyright 2020-present Brian O'Connor
// Copyright 2020-present Open Networking Foundation
// SPDX-License-Identifier: Apache-2.0

package p4rt

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	p4 "github.com/p4lang/p4runtime/go/p4/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type p4Write struct {
	req  *p4.WriteRequest
	resp chan []*p4.Error
}

type WriteTrace struct {
	BatchSize int
	Duration  time.Duration
	Errors    []*p4.Error
}

func (c *p4rtClient) Write(req *p4.WriteRequest) <-chan []*p4.Error {
	res := make(chan []*p4.Error, c.batchSize)
	c.writes <- p4Write{
		req:  proto.Clone(req).(*p4.WriteRequest),
		resp: res,
	}
	return res
}

func (c *p4rtClient) SetWriteTraceChan(traceChan chan WriteTrace) {
	c.writeTraceChan = traceChan
}

func (c *p4rtClient) ListenForWrites() {
	for {
		write := <-c.writes // wait for the first write in the batch
		req := write.req
		// Write the request
		start := time.Now()
		_, err := c.client.Write(context.Background(), req)
		// ignore the write response; it is an empty message (details, if any, are in err)
		go processWriteResponse(write, err, c.batchSize, start, c.writeTraceChan)
	}
}

func processWriteResponse(write p4Write, err error, batchSize int, start time.Time, traceChan chan WriteTrace) {
	duration := time.Since(start)
	errors := parseP4RuntimeWriteError(err, batchSize)
	// Send p4.Errors to waiting channels
	write.resp <- errors

	if traceChan != nil {
		trace := WriteTrace{
			BatchSize: batchSize,
			Duration:  duration,
			Errors:    errors,
		}
		select {
		case traceChan <- trace: // put trace into the channel unless it is full
		default:
			fmt.Println("Write trace channel full. Discarding trace")
		}
	}
}

func parseP4RuntimeWriteError(err error, batchSize int) []*p4.Error {
	errors := make([]*p4.Error, batchSize)
	var code int32
	var message = ""
	if err != nil {
		grpcError := status.Convert(err).Proto() // TODO consider status.FromError()
		if grpcError.GetCode() == int32(codes.Unknown) && batchSize > 0 && len(grpcError.GetDetails()) == batchSize {
			// gRPC error may contain p4.Errors
			for i := range grpcError.Details {
				p4Err := p4.Error{}
				unmarshallErr := ptypes.UnmarshalAny(grpcError.Details[i], &p4Err)
				if unmarshallErr != nil {
					// Unmarshalling p4.Error failed (construct a synthetic p4.Error)
					p4Err = p4.Error{
						CanonicalCode: int32(codes.Internal),
						Message:       unmarshallErr.Error(),
						Space:         "p4rt-go",
					}
				}
				errors[i] = &p4Err
			}
			return errors
		}
		message = grpcError.GetMessage()
	} else {
		code = int32(codes.OK)
	}

	// If the error does not have p4.Errors, build a stand-in p4.Error for all requests
	p4Error := &p4.Error{
		CanonicalCode: code,
		Message:       message,
	}
	for i := range errors {
		errors[i] = p4Error
	}
	return errors
}

func (c *p4rtClient) RemainingWrites() bool {
	return len(c.writes) > 0
}
