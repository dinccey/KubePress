package wordpress

import (
	"context"
	"fmt"
	crmv1 "hostzero.de/m/v2/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func ReconcilePHPMyAdmin(ctx context.Context, r client.Client, wp *crmv1.WordPressSite) error {
	logger := log.FromContext(ctx).WithValues("component", "phpmyadmin")

	logger = logger.WithValues("component", "phpmyadmin", "site", wp.Name, "namespace", wp.Namespace)

	deployment := &appsv1.Deployment{}
	deploymentName := "phpmyadmin"
	err := r.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: wp.Namespace}, deployment)

	if errors.IsNotFound(err) {
		env := []corev1.EnvVar{
			{
				Name:  "PMA_HOST",
				Value: fmt.Sprintf("%s.%s.svc.cluster.local", MariaDBClusterName, wp.Namespace),
			},
		}

		replicas := int32(1)

		deploymentLabels := map[string]string{
			"app.kubernetes.io/managed-by": "kubepress-operator",
			"app.kubernetes.io/part-of":    "kubepress",
			"app.kubernetes.io/name":       "phpmyadmin-server",
		}

		deployment = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentName,
				Namespace: wp.Namespace,
				Labels:    deploymentLabels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: deploymentLabels,
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: deploymentLabels,
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "phpmyadmin",
								Image: "phpmyadmin:latest",
								Env:   env,
								Ports: []corev1.ContainerPort{
									{
										Name:          "apache",
										ContainerPort: 80,
									},
								},
							},
						},
					},
				},
			},
		}

		if err := r.Create(ctx, deployment); err != nil {
			logger.Error(err, "Failed to create PHPMyAdmin deployment")
			return fmt.Errorf("failed to create PHPMyAdmin deployment %s: %w", deploymentName, err)
		}

		// --- Add Service definition ---
		serviceName := "phpmyadmin"
		serviceLabels := map[string]string{
			"app.kubernetes.io/managed-by": "kubepress-operator",
			"app.kubernetes.io/part-of":    "kubepress",
			"app.kubernetes.io/name":       "phpmyadmin-service",
		}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceName,
				Namespace: wp.Namespace,
				Labels:    serviceLabels,
			},
			Spec: corev1.ServiceSpec{
				Selector: deploymentLabels,
				Ports: []corev1.ServicePort{{
					Port:       80,
					TargetPort: intstr.FromInt(80),
				}},
				Type: corev1.ServiceTypeClusterIP,
			},
		}

		if err := r.Create(ctx, service); err != nil {
			if !errors.IsAlreadyExists(err) {
				logger.Error(err, "Failed to create PHPMyAdmin service")
				return fmt.Errorf("failed to create PHPMyAdmin service %s: %w", serviceName, err)
			}
		}

		// --- Add Ingress definition ---
		ingressName := "phpmyadmin"
		// Prefer explicit phpMyAdmin ingress settings from the CR; fall back to
		// the legacy PHPMYADMIN_DOMAIN env var for backward compatibility.
		host := os.Getenv("PHPMYADMIN_DOMAIN")
		var ingressClassName string
		var annotations map[string]string
		tlsEnabled := false
		if wp.Spec.PhpMyAdminIngress != nil {
			if wp.Spec.PhpMyAdminIngress.Host != "" {
				host = wp.Spec.PhpMyAdminIngress.Host
			}
			if wp.Spec.PhpMyAdminIngress.IngressClassName != "" {
				ingressClassName = wp.Spec.PhpMyAdminIngress.IngressClassName
			}
			if wp.Spec.PhpMyAdminIngress.Annotations != nil {
				annotations = make(map[string]string)
				for k, v := range wp.Spec.PhpMyAdminIngress.Annotations {
					annotations[k] = v
				}
			}
			tlsEnabled = wp.Spec.PhpMyAdminIngress.TLS
		}

		ingressLabels := map[string]string{
			"app.kubernetes.io/managed-by": "kubepress-operator",
			"app.kubernetes.io/part-of":    "kubepress",
			"app.kubernetes.io/name":       "phpmyadmin-ingress",
		}
		// Prepare annotations: start with defaults for the chosen class, then
		// overlay any user-provided annotations so they take precedence.
		expectedAnnotations := map[string]string{}
		if ingressClassName == "nginx" {
			expectedAnnotations["nginx.ingress.kubernetes.io/proxy-body-size"] = "32M"
			expectedAnnotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "100"
			expectedAnnotations["nginx.ingress.kubernetes.io/proxy-send-timeout"] = "100"
		}
		// overlay user annotations
		if annotations != nil {
			for k, v := range annotations {
				expectedAnnotations[k] = v
			}
		}

		ingress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:        ingressName,
				Namespace:   wp.Namespace,
				Labels:      ingressLabels,
				Annotations: expectedAnnotations,
			},
		}
		// Only set class when explicitly provided
		if ingressClassName != "" {
			ingress.Spec.IngressClassName = &ingressClassName
		}
		ingress.Spec.Rules = []networkingv1.IngressRule{{
			Host: host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path:     "/",
						PathType: func() *networkingv1.PathType { pt := networkingv1.PathTypePrefix; return &pt }(),
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: serviceName,
								Port: networkingv1.ServiceBackendPort{Number: 80},
							},
						},
					}},
				},
			},
		}}

		// Configure TLS when requested by the CR or when a cluster issuer is configured.
		if tlsEnabled || os.Getenv("TLS_CLUSTER_ISSUER") != "" {
			if issuer := os.Getenv("TLS_CLUSTER_ISSUER"); issuer != "" {
				ingress.Annotations["cert-manager.io/cluster-issuer"] = issuer
			}
			ingress.Spec.TLS = []networkingv1.IngressTLS{{
				Hosts:      []string{host},
				SecretName: GetTLSSecretName(wp.Name),
			}}
		}

		if err := r.Create(ctx, ingress); err != nil {
			if !errors.IsAlreadyExists(err) {
				logger.Error(err, "Failed to create PHPMyAdmin ingress")
				return fmt.Errorf("failed to create PHPMyAdmin ingress %s: %w", ingressName, err)
			}
		}
	} else if err != nil {
		logger.Error(err, "Failed to get PHPMyAdmin deployment")
		return fmt.Errorf("failed to get PHPMyAdmin deployment %s: %w", deploymentName, err)
	}

	return nil
}

// DeletePHPMyAdmin deletes the phpMyAdmin Deployment, Service, and Ingress for a WordPress site if they exist
func DeletePHPMyAdmin(ctx context.Context, r client.Client, wp *crmv1.WordPressSite) error {
	logger := log.FromContext(ctx).WithValues("component", "phpmyadmin-delete", "site", wp.Name, "namespace", wp.Namespace)
	var errs []error

	// Delete Deployment
	deployment := &appsv1.Deployment{}
	deploymentName := "phpmyadmin"
	if err := r.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: wp.Namespace}, deployment); err == nil {
		if err := r.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete PHPMyAdmin deployment")
			errs = append(errs, err)
		}
	}

	// Delete Service
	service := &corev1.Service{}
	serviceName := "phpmyadmin"
	if err := r.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: wp.Namespace}, service); err == nil {
		if err := r.Delete(ctx, service); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete PHPMyAdmin service")
			errs = append(errs, err)
		}
	}

	// Delete Ingress
	ingress := &networkingv1.Ingress{}
	ingressName := "phpmyadmin"
	if err := r.Get(ctx, types.NamespacedName{Name: ingressName, Namespace: wp.Namespace}, ingress); err == nil {
		if err := r.Delete(ctx, ingress); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete PHPMyAdmin ingress")
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("one or more errors occurred deleting phpMyAdmin resources")
	}
	return nil
}
