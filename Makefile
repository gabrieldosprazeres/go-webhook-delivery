.DEFAULT_GOAL := help

GO ?= go

.PHONY: help fmt fmt-check tidy-check test integration race vet staticcheck vuln openapi-lint build check \
	migration-validate migrate-up migrate-status compose-config compose-up compose-demo compose-down

help: ## Lista os comandos disponiveis.
	@awk 'BEGIN {FS = ":.*##"; printf "Uso: make <alvo>\n\n"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

fmt: ## Formata os arquivos Go.
	gofmt -w $$(rg --files -g '*.go')

fmt-check: ## Falha quando algum arquivo Go nao esta formatado.
	@test -z "$$(gofmt -l $$(rg --files -g '*.go'))"

tidy-check: ## Confere se go.mod e go.sum estao organizados, sem alterar arquivos.
	$(GO) mod tidy -diff

test: ## Executa os testes unitarios.
	$(GO) test ./...

integration: ## Executa schema, concorrencia e E2E; exige as quatro WDE_TEST_*_DATABASE_URL.
	@test -n "$$WDE_TEST_API_DATABASE_URL" -a -n "$$WDE_TEST_WORKER_DATABASE_URL" -a -n "$$WDE_TEST_ADMIN_DATABASE_URL" -a -n "$$WDE_TEST_SUPERUSER_DATABASE_URL"
	$(GO) test ./test/integration -run '^TestCredentialBootstrapIsSerializedAndRevocable$$' -count=1 -v
	$(GO) test ./test/integration -run '^(TestTenantContextAndAppendOnlyACL|TestConcurrentIdempotency|TestClaimWithoutCompleteSnapshotDoesNotMutateDelivery|TestEndpointEventWorkerChaosLabSucceeded|TestMigrationBoundariesRemainFailClosed|TestBatchClaimFairnessAndConcurrentWorkers|TestLockedWorkspaceDoesNotBlockIndependentClaim|TestClaimPlanUsesReadyIndexAtRepresentativeScale|TestPersistentFairnessAcrossSingleSlotCycles|TestPersistentFairnessWithConcurrentSingleSlotWorkers|TestEndpointCapacityDoesNotStarveHealthyEndpoint|TestLeaseRecoveryFencingAndAbandonedAttempt|TestRetryHistoryDeadLetterAndNeverMaxPlusOne|TestRepeatedCrashesStopAtMaximumAttempts|TestHostileHTTPStatusDoesNotBreakFinalizeOrNextTenant)$$' -count=1 -v
	$(GO) test ./test/integration -run '^(TestTenantTransactionDoesNotLeakAfterCommitRollbackOrPanic|TestPersistentQuotaIsAtomicAcrossDimensionsRestartAndExpiry|TestPersistentQuotaGlobalBucketContentionIsExact|TestFanoutAndPaginationLimitsAreEnforced|TestReplayGenerationIsConcurrentIdempotentAndPreservesHistory|TestReplayConflictAndPurgedPayloadFailClosed|TestReplayHTTPContractScopeAndTenantIsolation|TestOperationsACLAndAuditSnapshotsAreImmutable)$$' -count=1 -v
	$(GO) test ./test/integration -run '^(TestSecretRotationIsConcurrentIdempotentAndDualSigns|TestExpiredRotationIdempotencySurvivesSecretPurge|TestSecretStateAndTemporalConstraintsFailClosedForAPIAndOwner|TestPayloadPurgeFencesWorkerAndBlocksReplay|TestMaintenanceBatchesRejectNullAndEnforcePhysicalCap|TestRetentionRunnerDrainsLargeExpiredBucketBacklog|TestRetentionBacklogExcludesParentsBlockedByLiveChildren|TestMetadataRetentionPurgesGraphCommandsAndExpiredAudit|TestRestoreQuarantineRevokesSnapshotBeforeReadiness|TestWorkspacePurgePreservesTombstoneAndAudit|TestWorkspacePurgeSerializesRotationAndRewindsLateChildren|TestDataSecurityFunctionsAndRolesAreLeastPrivilege)$$' -count=1 -v
	$(GO) test ./cmd/api -run '^TestProductionRoutePipelineBoundsAuthScopeAndCrossTenantQuota$$' -count=1 -v
	$(GO) test ./cmd/worker -run '^TestWorkerProcessSIGTERM$$' -count=1 -v

race: ## Executa todos os testes com o race detector.
	$(GO) test -race ./...

vet: ## Executa as analises oficiais do Go.
	$(GO) vet ./...

staticcheck: ## Executa o Staticcheck pinado no modulo.
	$(GO) tool staticcheck ./...

vuln: ## Verifica vulnerabilidades alcancaveis no codigo Go.
	$(GO) tool govulncheck ./...

openapi-lint: ## Carrega, resolve referencias e valida semanticamente o contrato OpenAPI.
	$(GO) test ./api -run '^TestOpenAPIContract$$' -count=1

build: ## Compila os tres binarios em bin/.
	mkdir -p bin
	$(GO) build -trimpath -o bin/api ./cmd/api
	$(GO) build -trimpath -o bin/worker ./cmd/worker
	$(GO) build -trimpath -o bin/chaoslab ./cmd/chaoslab

migration-validate: ## Valida a sintaxe das migrations sem acessar o banco.
	$(GO) tool goose -dir db/migrations validate

migrate-up: ## Aplica migrations; exige WDE_MIGRATOR_DATABASE_URL.
	@test -n "$$WDE_MIGRATOR_DATABASE_URL" || (echo "WDE_MIGRATOR_DATABASE_URL e obrigatoria" >&2; exit 1)
	GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$WDE_MIGRATOR_DATABASE_URL" $(GO) tool goose -dir db/migrations up

migrate-status: ## Consulta migrations; exige WDE_MIGRATOR_DATABASE_URL.
	@test -n "$$WDE_MIGRATOR_DATABASE_URL" || (echo "WDE_MIGRATOR_DATABASE_URL e obrigatoria" >&2; exit 1)
	GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$WDE_MIGRATOR_DATABASE_URL" $(GO) tool goose -dir db/migrations status

compose-config: ## Valida o Compose local.
	docker compose --profile demo config --quiet

compose-up: ## Sobe PostgreSQL, migration explicita, API e worker.
	docker compose --profile core up --build

compose-demo: ## Sobe o core e o Chaos Lab local.
	docker compose --profile demo up --build

compose-down: ## Encerra os containers preservando o volume PostgreSQL.
	docker compose --profile demo down

check: fmt-check tidy-check test race vet staticcheck vuln openapi-lint migration-validate build ## Executa todos os gates locais da implementacao atual.
