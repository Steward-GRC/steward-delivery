// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package pdfrender

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// JobProjector updates pdf_jobs as a render's status changes;
// store.PDFJobRepo implements it.
type JobProjector interface {
	MarkProcessing(ctx context.Context, jobID string) error
	MarkDone(ctx context.Context, jobID, artifactKey string) error
	MarkFailed(ctx context.Context, jobID, errMsg string) error
}

// Informer watches PdfRender resources in one namespace and projects their
// status to the JobProjector, matching the resource name to the job id. The
// resource is the source of truth: a failed projection is reported and
// dropped, and the next update or resync repairs it.
type Informer struct {
	factory dynamicinformer.DynamicSharedInformerFactory
	project JobProjector
	onError func(error)
	stopCh  chan struct{}
}

// NewInformer returns an Informer ready to Run. onError gets each failed
// projection.
func NewInformer(dyn dynamic.Interface, namespace string, project JobProjector, onError func(error)) *Informer {
	if onError == nil {
		onError = func(error) {}
	}
	// The resync replays the cache, repairing a row that drifted while
	// delivery was down mid-render.
	f := dynamicinformer.NewFilteredDynamicSharedInformerFactory(dyn, 5*time.Minute, namespace, nil)
	i := &Informer{factory: f, project: project, onError: onError, stopCh: make(chan struct{})}
	_, _ = f.ForResource(GVR).Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    i.handle,
		UpdateFunc: func(_, newObj any) { i.handle(newObj) },
	})
	return i
}

func (i *Informer) handle(obj any) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	jobID := u.GetName()
	if jobID == "" {
		return
	}
	status := ReadStatus(u)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch status.Phase {
	case PhaseRunning:
		if err := i.project.MarkProcessing(ctx, jobID); err != nil {
			i.onError(fmt.Errorf("MarkProcessing %s: %w", jobID, err))
		}
	case PhaseSucceeded:
		artifactKey, _, _ := unstructured.NestedString(u.Object, "spec", "outputKey")
		if artifactKey == "" {
			artifactKey = status.OutputURL
		}
		if err := i.project.MarkDone(ctx, jobID, artifactKey); err != nil {
			i.onError(fmt.Errorf("MarkDone %s: %w", jobID, err))
		}
	case PhaseFailed:
		msg := status.Error
		if msg == "" {
			msg = "render failed (no error message from the renderer)"
		}
		if err := i.project.MarkFailed(ctx, jobID, msg); err != nil {
			i.onError(fmt.Errorf("MarkFailed %s: %w", jobID, err))
		}
	}
}

// Run starts the informer and blocks until ctx is cancelled. It fails if the
// first list never syncs.
func (i *Informer) Run(ctx context.Context) error {
	i.factory.Start(i.stopCh)
	for gvr, ok := range i.factory.WaitForCacheSync(i.stopCh) {
		if !ok {
			return fmt.Errorf("pdfrender: informer cache sync timed out for %s", gvr.String())
		}
	}
	<-ctx.Done()
	close(i.stopCh)
	return nil
}
