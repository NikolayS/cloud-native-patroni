/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/kube"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/postgres"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/sample"
)

// BootstrapLive runs the sole superuser operation before faults and then
// empirically verifies the checkpoint-function grant as cnpatroni_chaos.
func BootstrapLive(ctx context.Context, config Config, chaosPassword, superPassword string) error {
	if err := config.Validate(); err != nil {
		return err
	}
	kubeClient, err := kube.Build(config.Context)
	if err != nil {
		return err
	}
	if err := kubeClient.VerifyContext(ctx, config.Context); err != nil {
		return err
	}
	oracleNode := os.Getenv("NODE_NAME")
	if oracleNode == "" {
		return fmt.Errorf("NODE_NAME is required")
	}
	inventory, err := kubeClient.ExpectedPodInventory(ctx, config.Namespace, config.ExpectedNodes, oracleNode)
	if err != nil {
		return err
	}
	var leader *kube.PodInventory
	for index := range inventory {
		record := (sample.RESTSampler{Port: config.PatroniPort, Origin: time.Now(), Now: time.Now}).Sample(ctx, inventory[index].Name, inventory[index].IP, "/primary")
		if record.OK && record.StatusCode == http.StatusOK {
			if leader != nil {
				return fmt.Errorf("more than one Pod reports Patroni primary")
			}
			leader = &inventory[index]
		}
	}
	if leader == nil {
		return fmt.Errorf("no Pod reports Patroni primary")
	}
	superConfig, err := postgres.BuildConfig(leader.IP, uint16(config.PGPort), "postgres", "postgres", superPassword, config.EffectiveAttemptTimeout())
	if err != nil {
		return err
	}
	superConn, err := pgx.ConnectConfig(ctx, superConfig)
	if err != nil {
		return fmt.Errorf("connect bootstrap superuser to leader Pod IP: %w", err)
	}
	if err := postgres.Bootstrap(ctx, superConn, chaosPassword); err != nil {
		_ = superConn.Close(context.Background())
		return err
	}
	_ = superConn.Close(context.Background())
	chaosConfig, err := postgres.BuildConfig(leader.IP, uint16(config.PGPort), "postgres", "cnpatroni_chaos", chaosPassword, config.EffectiveAttemptTimeout())
	if err != nil {
		return err
	}
	chaosConn, err := pgx.ConnectConfig(ctx, chaosConfig)
	if err != nil {
		return fmt.Errorf("connect bootstrap verification role: %w", err)
	}
	defer chaosConn.Close(context.Background())
	verificationRound := time.Now().UnixNano()
	if _, err := chaosConn.Exec(ctx, postgres.CanonicalInsertSQL, pgx.QueryExecModeExec, verificationRound, "preflight-bootstrap-verification"); err != nil {
		return fmt.Errorf("checkpoint grant and canonical insert verification failed: %w", err)
	}
	return nil
}
