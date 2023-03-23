package nodeinventory

import (
	"context"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	apiGRPC "github.com/stackrox/scanner/api/grpc"
	v1 "github.com/stackrox/scanner/generated/scanner/api/v1"
	"github.com/stackrox/scanner/pkg/nodeinventory"
	"google.golang.org/grpc"
)

// Service defines the node scanning service.
type Service interface {
	apiGRPC.APIService

	v1.NodeInventoryServiceServer
}

// NewService returns the service for node scanning
func NewService(nodeName string) Service {
	// TODO(ROX-16095): Migrate env.DurationSetting into Scanner repo to use env vars for this config
	cachedCollector := nodeinventory.NewCachingScanner(
		&nodeinventory.Scanner{},
		"/cache/inventory-cache",
		3*time.Hour,
		30*time.Second,
		300*time.Second,
		func(duration time.Duration) { time.Sleep(duration) })

	return &serviceImpl{
		inventoryCollector: cachedCollector,
		nodeName:           nodeName,
	}
}

type serviceImpl struct {
	inventoryCollector nodeinventory.NodeInventorizer
	nodeName           string
}

func (s *serviceImpl) GetNodeInventory(ctx context.Context, req *v1.GetNodeInventoryRequest) (*v1.GetNodeInventoryResponse, error) {
	inventoryScan, err := s.inventoryCollector.Scan(s.nodeName)
	if err != nil {
		log.Errorf("Error running inventoryCollector.Scan(%s): %v", s.nodeName, err)
		return nil, errors.New("Internal scanner error: failed to scan node")
	}

	log.Debugf("InventoryScan: %+v", inventoryScan)

	return &v1.GetNodeInventoryResponse{
		NodeName:   s.nodeName,
		Components: inventoryScan.Components,
		Notes:      inventoryScan.Notes,
	}, nil
}

// RegisterServiceServer registers this service with the given gRPC Server.
func (s *serviceImpl) RegisterServiceServer(grpcServer *grpc.Server) {
	v1.RegisterNodeInventoryServiceServer(grpcServer, s)
}

// RegisterServiceHandler registers this service with the given gRPC Gateway endpoint.
func (s *serviceImpl) RegisterServiceHandler(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
	return v1.RegisterNodeInventoryServiceHandler(ctx, mux, conn)
}