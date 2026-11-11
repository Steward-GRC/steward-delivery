// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net"
	"testing"

	grpcactor "github.com/Bugs5382/go-grpc-actor"
	log "github.com/Bugs5382/go-log"
	workloadidentity "github.com/Bugs5382/go-workload-identity"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/config"
)

// actorEcho answers GetPDFDownloadLink with the actor the handler sees, so a
// test can tell whether the end user reached it.
type actorEcho struct {
	deliveryv1.UnimplementedDeliveryServiceServer
}

func (actorEcho) GetPDFDownloadLink(ctx context.Context, _ *deliveryv1.GetPDFDownloadLinkRequest) (*deliveryv1.GetPDFDownloadLinkResponse, error) {
	a, _ := grpcactor.FromContext(ctx)
	return &deliveryv1.GetPDFDownloadLinkResponse{SignedUrl: a.Subject}, nil
}

func serveEcho(t *testing.T, opts Options) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, lis, log.Nop(), opts, func(s *grpc.Server) { deliveryv1.RegisterDeliveryServiceServer(s, actorEcho{}) })
	}()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcactor.UnaryClientInterceptor()))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		cancel()
		<-done
	})
	return conn
}

func seenActor(t *testing.T, conn *grpc.ClientConn, ctx context.Context) string {
	t.Helper()
	resp, err := deliveryv1.NewDeliveryServiceClient(conn).GetPDFDownloadLink(grpcactor.WithActor(ctx, grpcactor.Actor{Subject: "bob"}), &deliveryv1.GetPDFDownloadLinkRequest{JobId: "job-1"})
	require.NoError(t, err)
	return resp.GetSignedUrl()
}

func TestTheGatewaysActorReachesTheHandler(t *testing.T) {
	iss := newLocalIssuer(t)
	v, err := workloadidentity.NewVerifier(config.StewardWorkload(workloadidentity.Config{
		Issuer: iss.url, CAFile: iss.caFile, AllowedServiceAccounts: []string{testNS + "/steward-gateway"},
	}), log.Nop())
	require.NoError(t, err)
	require.NoError(t, v.Refresh(context.Background()))
	conn := serveEcho(t, Options{Auth: &Auth{Verifier: v, Policy: workloadidentity.Policy{
		deliveryv1.DeliveryService_GetPDFDownloadLink_FullMethodName: {"gateway": workloadidentity.OnBehalf},
	}}})

	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+iss.token(t, "steward-gateway", "steward"))
	require.Equal(t, "bob", seenActor(t, conn, ctx))
}

func TestAnActorIsNotTrustedWithWorkloadAuthOff(t *testing.T) {
	conn := serveEcho(t, Options{})
	require.Empty(t, seenActor(t, conn, context.Background()), "no verified caller, so no user")
}
