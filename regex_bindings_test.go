package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	resourcepoolv1alpha1 "github.com/dynamia-ai/hami-enterprise/pkg/resourcepool/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestPoolRegexBindingLifecycle(t *testing.T) {
	ctx := t.Context()
	scheme := runtime.NewScheme()
	if err := resourcepoolv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pool := &resourcepoolv1alpha1.ResourcePool{ObjectMeta: metav1.ObjectMeta{Name: "pool-a", UID: "pool-a-uid"}}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool).Build()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		cmd := newRootCommand(&commandOptions{client: cli, out: &out, errOut: &out})
		cmd.SetArgs(append([]string{"pool"}, args...))
		err := cmd.ExecuteContext(ctx)
		return out.String(), err
	}
	mustRun := func(args ...string) string {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("%v: %v; output: %s", args, err, out)
		}
		return out
	}
	mustRun("regex-bindings", "create", "pool-a", "kube-.*")
	mustRun("regex-bindings", "create", "pool-b", "kube-.*")
	if _, err := run("regex-bindings", "create", "pool-a", "kube-.*"); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Fatalf("duplicate create error = %v", err)
	}
	if _, err := run("regex-bindings", "create", "pool-a", "["); err == nil || !strings.Contains(err.Error(), "invalid namespace regex") {
		t.Fatalf("invalid regex error = %v", err)
	}
	rules, err := listRegexBindings(ctx, cli, "pool-a", false)
	if err != nil || len(rules.Items) != 1 {
		t.Fatalf("list rules = %v, %v", rules, err)
	}
	rule := &rules.Items[0]
	id := rule.GetName()
	if !strings.HasPrefix(id, "rprb-") {
		t.Fatalf("unexpected generated ID: %s", id)
	}
	rule.SetUID("regex-uid") // The fake client doesn't set a UID on create.
	if err := cli.Update(ctx, rule); err != nil {
		t.Fatal(err)
	}
	qualified := strings.TrimSpace(mustRun("regex-bindings", "list", "--pool", "pool-a", "-o", "name"))
	mustRun("regex-bindings", "update", qualified, "team-.*")
	if got := mustRun("regex-bindings", "list", "--pool", "pool-a", "-o", "name"); strings.TrimSpace(got) != regexBindingResource+"/"+id {
		t.Fatalf("unexpected filtered list: %s", got)
	}
	if got := mustRun("describe", pool.Name); !strings.Contains(got, "Regex bindings:") || !strings.Contains(got, `"team-.*"`) {
		t.Fatalf("unexpected description: %s", got)
	}
	mustRun("delete", pool.Name, "--force")
	if err := cli.Get(ctx, client.ObjectKey{Name: id}, regexBindingObject()); !apierrors.IsNotFound(err) {
		t.Fatalf("get deleted rule: %v", err)
	}
	remaining, err := listRegexBindings(ctx, cli, "", false)
	if err != nil || len(remaining.Items) != 1 || regexBindingPool(&remaining.Items[0]) != "pool-b" {
		t.Fatalf("unexpected remaining rules: %v, %v", remaining, err)
	}
	other := &remaining.Items[0]
	other.SetUID("other-uid")
	if err := cli.Update(ctx, other); err != nil {
		t.Fatal(err)
	}
	mustRun("regex-bindings", "delete", strings.TrimSpace(mustRun("regex-bindings", "list", "--pool", "pool-b", "-o", "name")))
	if err := cli.Get(ctx, client.ObjectKey{Name: other.GetName()}, regexBindingObject()); !apierrors.IsNotFound(err) {
		t.Fatalf("get explicitly deleted rule: %v", err)
	}
}

func TestPoolRegexBindingPreview(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	var namespaces []client.Object
	for _, name := range []string{"team-b", "prefix-team-a", "team-a", "team-b-suffix"} {
		namespaces = append(namespaces, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	for _, tc := range []struct {
		name, pattern, want string
		forbidden           bool
	}{
		{name: "whole names and sorted matches", pattern: "team-a|team-b", want: "Pool: training\nMatching namespaces: 2\nteam-a\nteam-b\n"},
		{name: "zero matches", pattern: "absent", want: "Pool: training\nMatching namespaces: 0\n"},
		{name: "namespace list forbidden", pattern: ".*", forbidden: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespaces...).WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if tc.forbidden {
						return apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", nil)
					}
					return c.List(ctx, list, opts...)
				},
				Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
					t.Fatal("preview attempted to create a resource")
					return nil
				},
			}).Build()
			var out bytes.Buffer
			cmd := newRootCommand(&commandOptions{client: cli, out: &out, errOut: &out})
			cmd.SetArgs([]string{"pool", "regex-bindings", "create", "training", tc.pattern, "--preview"})
			err := cmd.ExecuteContext(t.Context())
			if tc.forbidden {
				if !apierrors.IsForbidden(err) || strings.Contains(out.String(), "Matching namespaces:") {
					t.Fatalf("list denied: err = %v, output = %q", err, out.String())
				}
				return
			}
			if err != nil || out.String() != tc.want {
				t.Fatalf("preview: err = %v, output = %q, want %q", err, out.String(), tc.want)
			}
		})
	}
}
