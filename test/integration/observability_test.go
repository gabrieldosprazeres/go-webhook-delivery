package integration

import (
	"context"
	"os"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
)

func TestQueueMetricsAreAggregateAndWorkerOnly(t *testing.T) {
	if os.Getenv("WDE_TEST_API_DATABASE_URL") == "" || os.Getenv("WDE_TEST_WORKER_DATABASE_URL") == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seedDeliveries(t, ctx, api, admin, materials, "queue-metrics", 3)
	stats, err := delivery.NewPostgresStore(worker).QueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Ready != 3 || stats.Oldest < 0 {
		t.Fatalf("unexpected aggregate queue stats: %+v", stats)
	}
	if _, err = api.Exec(context.Background(), `SELECT * FROM wde.delivery_queue_metrics()`); err == nil {
		t.Fatal("API role unexpectedly executed worker-only queue metrics")
	}
	var workerExecute, apiExecute, publicExecute bool
	if err = admin.QueryRow(ctx, `SELECT
		has_function_privilege('wde_worker','wde.delivery_queue_metrics()','EXECUTE'),
		has_function_privilege('wde_api','wde.delivery_queue_metrics()','EXECUTE'),
		has_function_privilege('public','wde.delivery_queue_metrics()','EXECUTE')`).
		Scan(&workerExecute, &apiExecute, &publicExecute); err != nil {
		t.Fatal(err)
	}
	if !workerExecute || apiExecute || publicExecute {
		t.Fatalf("unexpected ACL worker=%v api=%v public=%v", workerExecute, apiExecute, publicExecute)
	}
}
