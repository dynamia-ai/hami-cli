package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"

	resourcepoolv1alpha1 "github.com/dynamia-ai/hami-enterprise/pkg/resourcepool/api/v1alpha1"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const regexBindingKind = "ResourcePoolRegexBinding"
const regexBindingResource = "resourcepoolregexbinding.resource.hami.io"

func regexBindingObject() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(resourcepoolv1alpha1.GroupVersion.WithKind(regexBindingKind))
	return obj
}

func regexBindingPool(obj *unstructured.Unstructured) string {
	pool, _, _ := unstructured.NestedString(obj.Object, "spec", "poolRef", "name")
	return pool
}

func regexBindingPattern(obj *unstructured.Unstructured) string {
	pattern, _, _ := unstructured.NestedString(obj.Object, "spec", "namespaceRegex")
	return pattern
}

func listRegexBindings(ctx context.Context, cli client.Client, pool string, optional bool) (*unstructured.UnstructuredList, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(resourcepoolv1alpha1.GroupVersion.WithKind(regexBindingKind + "List"))
	if err := cli.List(ctx, list); err != nil {
		if optional && (meta.IsNoMatchError(err) || apierrors.IsNotFound(err)) {
			return list, nil
		}
		return nil, fmt.Errorf("list ResourcePoolRegexBindings: %w", err)
	}
	if pool != "" {
		items := list.Items[:0]
		for i := range list.Items {
			if regexBindingPool(&list.Items[i]) == pool {
				items = append(items, list.Items[i])
			}
		}
		list.Items = items
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].GetName() < list.Items[j].GetName() })
	return list, nil
}

func checkRegexBindingDuplicate(list *unstructured.UnstructuredList, pool, pattern, exclude string) error {
	for i := range list.Items {
		obj := &list.Items[i]
		if obj.GetName() != exclude && regexBindingPattern(obj) == pattern {
			return fmt.Errorf("resource pool %q already has namespace regex %q", pool, pattern)
		}
	}
	return nil
}

func validateNamespaceRegex(pattern string) error {
	if len(pattern) == 0 || len(pattern) > 256 {
		return fmt.Errorf("namespace regex must contain 1 to 256 bytes")
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("invalid namespace regex: %w", err)
	}
	return nil
}

func createRegexBinding(ctx context.Context, cli client.Client, pool, pattern string) (*unstructured.Unstructured, error) {
	if err := validateNamespaceRegex(pattern); err != nil {
		return nil, err
	}
	list, err := listRegexBindings(ctx, cli, pool, false)
	if err != nil {
		return nil, err
	}
	if err := checkRegexBindingDuplicate(list, pool, pattern, ""); err != nil {
		return nil, err
	}
	// Match Kantaloupe's ID derivation so identical concurrent creates contend
	// for the same name. Edits retain their ID, so probe an occupied name.
	for i := 0; i <= len(list.Items); i++ {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%q:%q:%d", pool, pattern, i)))
		obj := regexBindingObject()
		obj.SetName(fmt.Sprintf("rprb-%x", hash[:16]))
		obj.Object["spec"] = map[string]any{"poolRef": map[string]any{"name": pool}, "namespaceRegex": pattern}
		if err := cli.Create(ctx, obj); err == nil {
			return obj, nil
		} else if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("create ResourcePoolRegexBinding: %w", err)
		}
		existing := regexBindingObject()
		if err := cli.Get(ctx, client.ObjectKey{Name: obj.GetName()}, existing); err != nil {
			return nil, fmt.Errorf("re-read conflicting ResourcePoolRegexBinding: %w", err)
		}
		if regexBindingPool(existing) == pool && regexBindingPattern(existing) == pattern {
			return nil, fmt.Errorf("resource pool %q already has namespace regex %q", pool, pattern)
		}
	}
	return nil, fmt.Errorf("could not allocate a rule ID for resource pool %q; retry creation", pool)
}

func deleteRegexBinding(ctx context.Context, cli client.Client, obj *unstructured.Unstructured) error {
	uid, version := obj.GetUID(), obj.GetResourceVersion()
	if uid == "" || version == "" {
		return fmt.Errorf("ResourcePoolRegexBinding %q has no UID or resource version", obj.GetName())
	}
	if err := cli.Delete(ctx, obj, client.Preconditions{UID: &uid, ResourceVersion: &version}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete ResourcePoolRegexBinding %q: %w", obj.GetName(), err)
	}
	return nil
}

func newPoolRegexBindingsCommand(options *commandOptions) *cobra.Command {
	group := &cobra.Command{Use: "regex-bindings", Short: "Manage namespace regex bindings for resource pools"}
	var pool string
	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List regex binding rules", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			rules, err := listRegexBindings(cmd.Context(), cli, pool, false)
			if err != nil {
				return err
			}
			output, _ := cmd.Flags().GetString("output")
			if output != "table" {
				names := make([]string, 0, len(rules.Items))
				for i := range rules.Items {
					names = append(names, regexBindingResource+"/"+rules.Items[i].GetName())
				}
				return printObject(options.out, output, rules, names)
			}
			w := newTableWriter(options.out)
			fmt.Fprintln(w, "ID\tPOOL\tNAMESPACE REGEX")
			for i := range rules.Items {
				rule := &rules.Items[i]
				fmt.Fprintf(w, "%s\t%s\t%q\n", rule.GetName(), regexBindingPool(rule), regexBindingPattern(rule))
			}
			return w.Flush()
		},
	}
	list.Flags().StringVar(&pool, "pool", "", "List rules for this resource pool only")
	addOutputFlag(list)
	create := &cobra.Command{
		Use: "create POOL REGEX", Short: "Grant pool access to namespaces matching a full RE2 expression", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			rule, err := createRegexBinding(cmd.Context(), cli, args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(options.out, "%s/%s created\n", regexBindingResource, rule.GetName())
			return nil
		},
	}
	update := &cobra.Command{
		Use: "update ID REGEX", Short: "Change a rule's regex without changing its ID or pool", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateNamespaceRegex(args[1]); err != nil {
				return err
			}
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			rule := regexBindingObject()
			if err := cli.Get(ctx, client.ObjectKey{Name: args[0]}, rule); err != nil {
				return fmt.Errorf("get ResourcePoolRegexBinding %q: %w", args[0], err)
			}
			pool := regexBindingPool(rule)
			list, err := listRegexBindings(ctx, cli, pool, false)
			if err != nil {
				return err
			}
			if err := checkRegexBindingDuplicate(list, pool, args[1], args[0]); err != nil {
				return err
			}
			if err := unstructured.SetNestedField(rule.Object, args[1], "spec", "namespaceRegex"); err != nil {
				return err
			}
			if err := cli.Update(ctx, rule); err != nil {
				return fmt.Errorf("update ResourcePoolRegexBinding %q: %w", args[0], err)
			}
			fmt.Fprintf(options.out, "%s/%s updated\n", regexBindingResource, args[0])
			return nil
		},
	}
	remove := &cobra.Command{
		Use: "delete ID", Short: "Delete a rule by its ID", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			rule := regexBindingObject()
			if err := cli.Get(cmd.Context(), client.ObjectKey{Name: args[0]}, rule); err != nil {
				return fmt.Errorf("get ResourcePoolRegexBinding %q: %w", args[0], err)
			}
			if err := deleteRegexBinding(cmd.Context(), cli, rule); err != nil {
				return err
			}
			fmt.Fprintf(options.out, "%s/%s deleted\n", regexBindingResource, args[0])
			return nil
		},
	}
	group.AddCommand(list, create, update, remove)
	return group
}
