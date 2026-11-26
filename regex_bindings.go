package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"

	resourcepoolv1alpha1 "github.com/dynamia-ai/hami-cli/internal/resourcepool/v1alpha1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
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

func compileNamespaceRegex(pattern string) (*regexp.Regexp, error) {
	if len(pattern) == 0 || len(pattern) > 256 {
		return nil, fmt.Errorf("namespace regex must contain 1 to 256 bytes")
	}
	// Anchor the parsed expression exactly as Resource Pool Manager does.
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, fmt.Errorf("invalid namespace regex: %w", err)
	}
	return regexp.Compile(`\A(?:` + parsed.String() + `)\z`)
}

func previewRegexBinding(ctx context.Context, cli client.Reader, out io.Writer, pool string, pattern *regexp.Regexp) error {
	var namespaces corev1.NamespaceList
	if err := cli.List(ctx, &namespaces); err != nil {
		return fmt.Errorf("list namespaces for preview: %w", err)
	}
	var matches []string
	for i := range namespaces.Items {
		name := namespaces.Items[i].Name
		if pattern.MatchString(name) {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	fmt.Fprintf(out, "Pool: %s\nMatching namespaces: %d\n", pool, len(matches))
	for _, name := range matches {
		fmt.Fprintln(out, name)
	}
	return nil
}

func createRegexBinding(ctx context.Context, cli client.Client, pool, pattern string) (*unstructured.Unstructured, error) {
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
	var preview bool
	create := &cobra.Command{
		Use: "create POOL REGEX", Short: "Grant pool access to namespaces matching a full RE2 expression", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pattern, err := compileNamespaceRegex(args[1])
			if err != nil {
				return err
			}
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			if preview {
				return previewRegexBinding(cmd.Context(), cli, options.out, args[0], pattern)
			}
			rule, err := createRegexBinding(cmd.Context(), cli, args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(options.out, "%s/%s created\n", regexBindingResource, rule.GetName())
			return nil
		},
	}
	create.Flags().BoolVar(&preview, "preview", false, "List and count currently matching namespaces without creating a rule")
	update := &cobra.Command{
		Use: "update ID_OR_RESOURCE_NAME REGEX", Short: "Change a rule's regex without changing its ID or pool", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := compileNamespaceRegex(args[1]); err != nil {
				return err
			}
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			id := strings.TrimPrefix(args[0], regexBindingResource+"/")
			rule := regexBindingObject()
			if err := cli.Get(ctx, client.ObjectKey{Name: id}, rule); err != nil {
				return fmt.Errorf("get ResourcePoolRegexBinding %q: %w", id, err)
			}
			pool := regexBindingPool(rule)
			list, err := listRegexBindings(ctx, cli, pool, false)
			if err != nil {
				return err
			}
			if err := checkRegexBindingDuplicate(list, pool, args[1], id); err != nil {
				return err
			}
			if err := unstructured.SetNestedField(rule.Object, args[1], "spec", "namespaceRegex"); err != nil {
				return err
			}
			if err := cli.Update(ctx, rule); err != nil {
				return fmt.Errorf("update ResourcePoolRegexBinding %q: %w", id, err)
			}
			fmt.Fprintf(options.out, "%s/%s updated\n", regexBindingResource, id)
			return nil
		},
	}
	remove := &cobra.Command{
		Use: "delete ID_OR_RESOURCE_NAME", Short: "Delete a rule by its ID", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := options.getClient()
			if err != nil {
				return err
			}
			id := strings.TrimPrefix(args[0], regexBindingResource+"/")
			rule := regexBindingObject()
			if err := cli.Get(cmd.Context(), client.ObjectKey{Name: id}, rule); err != nil {
				return fmt.Errorf("get ResourcePoolRegexBinding %q: %w", id, err)
			}
			if err := deleteRegexBinding(cmd.Context(), cli, rule); err != nil {
				return err
			}
			fmt.Fprintf(options.out, "%s/%s deleted\n", regexBindingResource, id)
			return nil
		},
	}
	group.AddCommand(list, create, update, remove)
	return group
}
