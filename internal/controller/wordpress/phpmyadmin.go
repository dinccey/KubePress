package wordpress

import (
	"context"
	"fmt"
	"os"
	"strings"

	crmv1 "hostzero.de/m/v2/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ReconcilePHPMyAdmin ensures a single, global phpMyAdmin Deployment/Service/Ingress
// exists. Minimal, robust implementation.
func ReconcilePHPMyAdmin(ctx context.Context, r client.Client, wp *crmv1.WordPressSite) error {
	logger := log.FromContext(ctx).WithValues("component", "phpmyadmin")

	// Global phpMyAdmin mode can be controlled by env var. Default: enabled.
	if strings.ToLower(os.Getenv("PHPMYADMIN_ENABLED")) == "false" {
		logger.V(1).Info("Global phpMyAdmin disabled via PHPMYADMIN_ENABLED")
		return nil
	}

	// If site requests disabling phpMyAdmin, delete the global Ingress
	if wp.Spec.DisablePhpMyAdmin {
		logger.Info("Site requests phpMyAdmin disabled; deleting global ingress", "site", wp.Name)
		targetNS := os.Getenv("PHPMYADMIN_NAMESPACE")
		if targetNS == "" {
			targetNS = wp.Namespace
		}
		ing := &networkingv1.Ingress{}
		if err := r.Get(ctx, types.NamespacedName{Name: "phpmyadmin", Namespace: targetNS}, ing); err == nil {
			if err := r.Delete(ctx, ing); err != nil {
				logger.Error(err, "failed to delete phpMyAdmin ingress")
				return err
			}
			logger.Info("deleted phpMyAdmin ingress", "namespace", targetNS)
		} else if !kerrors.IsNotFound(err) {
			logger.Error(err, "error checking phpMyAdmin ingress for deletion")
			return err
		}
		return nil
	}

	// Determine target namespace for the global phpMyAdmin resources.
	targetNS := os.Getenv("PHPMYADMIN_NAMESPACE")
	if targetNS == "" {
		targetNS = wp.Namespace
	}

	// Determine MariaDB namespace for PMA_HOST.
	mariadbNS := os.Getenv("PHPMYADMIN_MARIADB_NAMESPACE")
	if mariadbNS == "" {
		mariadbNS = wp.Namespace
	}

	logger = logger.WithValues("phpmyadmin-namespace", targetNS, "mariadb-namespace", mariadbNS)

	deploymentName := "phpmyadmin"
	serviceName := "phpmyadmin"
	ingressName := "phpmyadmin"

	// Ensure Deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: targetNS}, deployment); err != nil {
		if kerrors.IsNotFound(err) {
			envVars := []corev1.EnvVar{
				{Name: "PMA_HOST", Value: fmt.Sprintf("%s.%s.svc.cluster.local", MariaDBClusterName, mariadbNS)},
			}

			replicas := int32(1)
			labels := map[string]string{
				"app.kubernetes.io/managed-by": "kubepress-operator",
				"app.kubernetes.io/part-of":    "kubepress",
				"app.kubernetes.io/name":       "phpmyadmin-server",
			}

			image := os.Getenv("PHPMYADMIN_IMAGE")
			if image == "" {
				image = "phpmyadmin:latest"
			}

			deployment = &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: targetNS,
					Labels:    labels,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{MatchLabels: labels},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "phpmyadmin",
									Image: image,
									Env:   envVars,
									Ports: []corev1.ContainerPort{
										{Name: "apache", ContainerPort: 80},
									},
								},
							},
						},
					},
				},
			}

			if err := r.Create(ctx, deployment); err != nil {
				logger.Error(err, "Failed to create global phpMyAdmin Deployment")
				return fmt.Errorf("failed to create global phpMyAdmin deployment: %w", err)
			}
			logger.Info("Created global phpMyAdmin Deployment", "name", deploymentName, "namespace", targetNS)
		} else {
			logger.Error(err, "Failed to get phpMyAdmin Deployment")
			return fmt.Errorf("failed to get phpMyAdmin deployment: %w", err)
		}
	}

	// Ensure Service
	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: targetNS}, svc); err != nil {
		if kerrors.IsNotFound(err) {
			svc = &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: targetNS,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "kubepress-operator",
						"app.kubernetes.io/part-of":    "kubepress",
						"app.kubernetes.io/name":       "phpmyadmin-service",
					},
				},
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"app.kubernetes.io/name": "phpmyadmin-server"},
					Ports: []corev1.ServicePort{
						{Port: 80, TargetPort: intstr.FromInt(80)},
					},
					Type: corev1.ServiceTypeClusterIP,
				},
			}

			if err := r.Create(ctx, svc); err != nil {
				logger.Error(err, "Failed to create phpMyAdmin service")
				return fmt.Errorf("failed to create phpMyAdmin service: %w", err)
			}
			logger.Info("Created phpMyAdmin Service", "name", serviceName, "namespace", targetNS)
		} else {
			logger.Error(err, "Failed to get phpMyAdmin Service")
			return fmt.Errorf("failed to get phpMyAdmin service: %w", err)
		}
	}

	// Ensure Ingress
	ing := &networkingv1.Ingress{}
	if err := r.Get(ctx, types.NamespacedName{Name: ingressName, Namespace: targetNS}, ing); err != nil {
		if kerrors.IsNotFound(err) {
			host := os.Getenv("PHPMYADMIN_DOMAIN")
			if host == "" {
				logger.Info("PHPMYADMIN_DOMAIN not set; skipping Ingress creation")
				return nil
			}

			ingressClass := os.Getenv("PHPMYADMIN_INGRESS_CLASS")
			annotations := map[string]string{}

			if ingressClass == "nginx" {
				annotations["nginx.ingress.kubernetes.io/proxy-body-size"] = "32M"
				annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "100"
				annotations["nginx.ingress.kubernetes.io/proxy-send-timeout"] = "100"
			}
			if issuer := os.Getenv("TLS_CLUSTER_ISSUER"); issuer != "" {
				annotations["cert-manager.io/cluster-issuer"] = issuer
			}

			pathType := networkingv1.PathTypePrefix

			ing = &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:        ingressName,
					Namespace:   targetNS,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "kubepress-operator",
						"app.kubernetes.io/part-of":    "kubepress",
						"app.kubernetes.io/name":       "phpmyadmin-ingress",
					},
					Annotations: annotations,
				},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{
						{
							Host: host,
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{
											Path:     "/",
											PathType: &pathType,
											Backend: networkingv1.IngressBackend{
												Service: &networkingv1.IngressServiceBackend{
													Name: serviceName,
													Port: networkingv1.ServiceBackendPort{
														Number: 80,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}

			if ingressClass != "" {
				ing.Spec.IngressClassName = &ingressClass
			}

			if os.Getenv("TLS_CLUSTER_ISSUER") != "" || os.Getenv("PHPMYADMIN_TLS") == "true" {
				ing.Spec.TLS = []networkingv1.IngressTLS{
					{
						Hosts:      []string{host},
						SecretName: GetTLSSecretName("phpmyadmin"),
					},
				}
			}

			if err := r.Create(ctx, ing); err != nil {
				logger.Error(err, "Failed to create phpMyAdmin ingress")
				return fmt.Errorf("failed to create phpMyAdmin ingress: %w", err)
			}
			logger.Info("Created phpMyAdmin Ingress", "name", ingressName, "namespace", targetNS, "host", host)
		} else {
			logger.Error(err, "Failed to get phpMyAdmin Ingress")
			return fmt.Errorf("failed to get phpMyAdmin ingress: %w", err)
		}
	}

	return nil
}

// DeletePHPMyAdmin performs best-effort deletion of global phpMyAdmin resources
// when PHPMYADMIN_ENABLED is explicitly set to "false".
func DeletePHPMyAdmin(ctx context.Context, r client.Client) error {
	logger := log.FromContext(ctx).WithValues("component", "phpmyadmin-delete")

	if strings.ToLower(os.Getenv("PHPMYADMIN_ENABLED")) != "false" {
		return nil
	}

	targetNS := os.Getenv("PHPMYADMIN_NAMESPACE")
	if targetNS == "" {
		logger.V(1).Info("PHPMYADMIN_NAMESPACE not set; skipping deletion")
		return nil
	}

	objects := []client.Object{
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "phpmyadmin", Namespace: targetNS}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "phpmyadmin", Namespace: targetNS}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "phpmyadmin", Namespace: targetNS}},
	}

	for _, obj := range objects {
		if err := r.Delete(ctx, obj); err != nil && !kerrors.IsNotFound(err) {
			logger.Error(err, "Failed to delete phpMyAdmin resource", "kind", obj.GetObjectKind().GroupVersionKind())
		}
	}

	logger.Info("Requested deletion of global phpMyAdmin resources", "namespace", targetNS)
	return nil
}