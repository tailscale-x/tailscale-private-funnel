package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestControlTransferDatabaseMatrix(t *testing.T) {
	for _, sourcePG := range []bool{false, true} {
		for _, destPG := range []bool{false, true} {
			t.Run(map[bool]string{false: "sqlite", true: "postgres"}[sourcePG]+"-to-"+map[bool]string{false: "sqlite", true: "postgres"}[destPG], func(t *testing.T) {
				src, _, _ := contractStore(t, sourcePG)
				if err := src.SaveProvider("provider-id", "cloudflare", map[string]string{"api_token": "private-token"}, true, "Provider display"); err != nil {
					t.Fatal(err)
				}
				if err := src.SetSecret("tailscale_oauth_client_id", "oauth-id"); err != nil {
					t.Fatal(err)
				}
				_, originalKey, err := src.ConfigurationKey()
				if err != nil {
					t.Fatal(err)
				}
				if _, err = src.db.Exec(`INSERT INTO audit(id,action,actor,created_at) VALUES(77,'fixture','admin',1)`); err != nil {
					t.Fatal(err)
				}
				if _, err = src.db.Exec(`INSERT INTO records(id,provider_id,zone,name,type,value,owner_id) VALUES('r','provider-id','example.com','app','CNAME','lb.example.com','exact-owner')`); err != nil {
					t.Fatal(err)
				}
				if _, err = src.db.Exec(`INSERT INTO nodes(id,mode,hostname) VALUES('parent','k8s-gateway','node')`); err != nil {
					t.Fatal(err)
				}
				if _, err = src.db.Exec(`INSERT INTO nodes(id,mode,hostname) VALUES('child','k8s-forwarder','child')`); err != nil {
					t.Fatal(err)
				}
				if _, err = src.db.Exec(`INSERT INTO kubernetes_forwarders(node_id,parent_id,kubernetes_node_uid) VALUES('child','parent','node-uid')`); err != nil {
					t.Fatal(err)
				}

				for _, q := range []string{
					`INSERT INTO applications(id,service_name,service_fqdn,service_port,backend_port,created_at,updated_at) VALUES('app','svc:fixture','fixture.ts.net',80,8080,1,2)`,
					`INSERT INTO backend_assignments(id,application_id,node_id,container_id,backend_ip,backend_port) VALUES('assignment','app','parent','container','10.0.0.2',8080)`,
					`INSERT INTO enrollments(id,mode,hostname,code_hash,created_at) VALUES('enrollment','tailnet-gateway','fixture','consumed-hash',1)`,
					`INSERT INTO exposures(id,application_id,hostname) VALUES('exposure','app','app.example.com')`,
					`INSERT INTO managed_zones(id,provider_id,zone) VALUES('zone-id','provider-id','example.com')`,
					`INSERT INTO report_nonces(node_id,nonce,received_at) VALUES('parent','already-used',1)`,
					`INSERT INTO reports(node_id,payload,received_at) VALUES('parent','{}',1)`,
					`INSERT INTO service_hosts(service_name,device_id,approved,approval_state,updated_at) VALUES('svc:fixture','device',1,'approved',1)`,
				} {
					if _, err = src.db.Exec(q); err != nil {
						t.Fatal(err)
					}
				}
				snap, err := src.ExportTransfer(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(snap)
				if strings.Contains(string(raw), "private-token") {
					t.Fatal("plaintext credential exported")
				}
				var opts DatabaseOptions
				dir := t.TempDir()
				if destPG {
					opts, dir = postgresContractOptions(t)
				}
				if err = os.WriteFile(filepath.Join(dir, "master.key"), src.key, 0600); err != nil {
					t.Fatal(err)
				}
				dest, err := OpenConfigured(dir, "", opts)
				if err != nil {
					t.Fatal(err)
				}
				defer dest.Close()
				if err = dest.ImportTransfer(context.Background(), snap); err != nil {
					t.Fatal(err)
				}
				result, err := dest.ExportTransfer(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(snap, result) {
					a, _ := json.Marshal(snap)
					b, _ := json.Marshal(result)
					t.Fatalf("transfer data changed\n%s\n%s", a, b)
				}
				if value, err := dest.GetSecret("tailscale_oauth_client_id"); err != nil || value != "oauth-id" {
					t.Fatal("credential identity changed", err)
				}
				_, key, err := dest.ConfigurationKey()
				if err != nil || !reflect.DeepEqual(originalKey, key) {
					t.Fatal("signing identity changed", err)
				}
				if err = dest.ImportTransfer(context.Background(), snap); err == nil {
					t.Fatal("occupied destination accepted")
				}
				if _, err = dest.db.Exec(`INSERT INTO audit(action,actor,created_at) VALUES('restored','admin',2)`); err != nil {
					t.Fatal(err)
				}
				var max int
				if err = dest.db.QueryRow(`SELECT max(id) FROM audit`).Scan(&max); err != nil || max <= 77 {
					t.Fatal("audit sequence not restored", max, err)
				}
			})
		}
	}
}
func TestTransferRejectsCorruptionAndRollsBack(t *testing.T) {
	src, _, _ := contractStore(t, false)
	src.SetSecret("secret", "value")
	snap, err := src.ExportTransfer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range snap.Tables {
		if snap.Tables[i].Name == "nodes" {
			snap.Tables[i].Rows = [][]TransferValue{{}}
		}
	}
	dest, _, _ := contractStore(t, false)
	dest.key = src.key
	if err = dest.ImportTransfer(context.Background(), snap); err == nil {
		t.Fatal("corrupt row accepted")
	}
	var count int
	dest.db.QueryRow(`SELECT count(*) FROM settings`).Scan(&count)
	if count != 0 {
		t.Fatal("partial data imported")
	}
	// Inject a late duplicate to exercise transaction rollback after earlier writes.
	snap, _ = src.ExportTransfer(context.Background())
	for i := range snap.Tables {
		if snap.Tables[i].Name == "settings" && len(snap.Tables[i].Rows) > 0 {
			snap.Tables[i].Rows = append(snap.Tables[i].Rows, snap.Tables[i].Rows[0])
		}
	}
	if err = dest.ImportTransfer(context.Background(), snap); err == nil {
		t.Fatal("duplicate accepted")
	}
	dest.db.QueryRow(`SELECT count(*) FROM settings`).Scan(&count)
	if count != 0 {
		t.Fatal("failed transaction retained data")
	}
}
func TestTransferredDatabaseCannotReopen(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, o, dir := contractStore(t, pg)
			if err := s.MarkTransferred("transfer-id"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if again, err := OpenConfigured(dir, "", o); err == nil {
				again.Close()
				t.Fatal("transferred source reopened")
			}
		})
	}
}
