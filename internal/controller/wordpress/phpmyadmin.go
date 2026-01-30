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
// exists. Configuration is driven by controller environment variables so that a
// single domain (defined at chart install) can be used.
func ReconcilePHPMyAdmin(ctx context.Context, r client.Client, wp *crmv1.WordPressSite) error {
	logger := log.FromContext(ctx).WithValues("component", "phpmyadmin")

	// Allow cluster operators to disable the global phpMyAdmin entirely
	if strings.ToLower(os.Getenv("PHPMYADMIN_ENABLED")) == "false" {
		logger.V(1).Info("Global phpMyAdmin disabled via PHPMYADMIN_ENABLED")
		return nil
	}

	// target namespace for phpMyAdmin resources (env or fallback to site namespace)
	targetNS := os.Getenv("PHPMYADMIN_NAMESPACE")
	if targetNS == "" {
		targetNS = wp.Namespace
	}

	// namespace where the MariaDB cluster to connect to lives
	mariadbNS := os.Getenv("PHPMYADMIN_MARIADB_NAMESPACE")
	if mariadbNS == "" {
		mariadbNS = wp.Namespace
	}

	logger = logger.WithValues("phpmyadmin-namespace", targetNS, "mariadb-namespace", mariadbNS)

	// resource names
	deploymentName := "phpmyadmin"
	serviceName := "phpmyadmin"
	ingressName := "phpmyadmin"

	// --- Deployment ---
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: targetNS}, dep); err != nil {
		if !kerrors.IsNotFound(err) {
			logger.Error(err, "failed to query phpMyAdmin deployment")
			return err
		}

		// create deployment
		envVars := []corev1.EnvVar{{Name: "PMA_HOST", Value: fmt.Sprintf("%s.%s.svc.cluster.local", MariaDBClusterName, mariadbNS)}}
		replicas := int32(1)
		labels := map[string]string{"app.kubernetes.io/managed-by": "kubepress-operator", "app.kubernetes.io/part-of": "kubepress", "app.kubernetes.io/name": "phpmyadmin-server"}

		image := os.Getenv("PHPMYADMIN_IMAGE")
		if image == "" {
			image = "phpmyadmin:latest"
		}

		dep = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: deploymentName, Namespace: targetNS, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "phpmyadmin", Image: image, Env: envVars, Ports: []corev1.ContainerPort{{Name: "apache", ContainerPort: 80}}}}}},
			},
		}

		if err := r.Create(ctx, dep); err != nil {
			logger.Error(err, "failed to create phpMyAdmin deployment")
			return err
		}
		logger.Info("created phpMyAdmin Deployment", "namespace", targetNS)
	}

	// --- Service ---
	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: targetNS}, svc); err != nil {
		if !kerrors.IsNotFound(err) {
			logger.Error(err, "failed to query phpMyAdmin service")
			return err
		}

		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: targetNS, Labels: map[string]string{"app.kubernetes.io/managed-by": "kubepress-operator", "app.kubernetes.io/part-of": "kubepress", "app.kubernetes.io/name": "phpmyadmin-service"}},
			Spec: corev1.ServiceSpec{Selector: map[string]string{"app.kubernetes.io/name": "phpmyadmin-server"}, Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(80)}}, Type: corev1.ServiceTypeClusterIP},
		}

		if err := r.Create(ctx, svc); err != nil {
			logger.Error(err, "failed to create phpMyAdmin service")
			return err
		}
		logger.Info("created phpMyAdmin Service", "namespace", targetNS)
	}

	// --- Ingress ---
	ing := &networkingv1.Ingress{}
	if err := r.Get(ctx, types.NamespacedName{Name: ingressName, Namespace: targetNS}, ing); err != nil {
		if !kerrors.IsNotFound(err) {
			logger.Error(err, "failed to query phpMyAdmin ingress")
			return err
		}

		host := os.Getenv("PHPMYADMIN_DOMAIN")
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

		ing = &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: ingressName, Namespace: targetNS, Labels: map[string]string{"app.kubernetes.io/managed-by": "kubepress-operator", "app.kubernetes.io/part-of": "kubepress", "app.kubernetes.io/name": "phpmyadmin-ingress"}, Annotations: annotations},
			Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: host, IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: func() *networkingv1.PathType { pt := networkingv1.PathTypePrefix; return &pt }(), Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: serviceName, Port: networkingv1.ServiceBackendPort{Number: 80}}}}}}}}},
		}

		if ingressClass != "" {
			ing.Spec.IngressClassName = &ingressClass
		}
		if os.Getenv("TLS_CLUSTER_ISSUER") != "" || os.Getenv("PHPMYADMIN_TLS") == "true" {
			ing.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{os.Getenv("PHPMYADMIN_DOMAIN")}, SecretName: GetTLSSecretName("phpmyadmin")}}
		}

		if err := r.Create(ctx, ing); err != nil {
			logger.Error(err, "failed to create phpMyAdmin ingress")
			return err
		}
		logger.Info("created phpMyAdmin Ingress", "namespace", targetNS)
	}

	return nil
}

// DeletePHPMyAdmin deletes the global phpMyAdmin resources if explicitly requested.
// By default it is a no-op because global lifecycle should be managed via controller
// env vars or the chart rather than individual WordPressSite CRs.
func DeletePHPMyAdmin(ctx context.Context, r client.Client) error {
	logger := log.FromContext(ctx).WithValues("component", "phpmyadmin-delete")

	if strings.ToLower(os.Getenv("PHPMYADMIN_ENABLED")) == "false" {
		// Attempt deletion if disabled
		targetNS := os.Getenv("PHPMYADMIN_NAMESPACE")
		// if empty, nothing to do because deployment was created in site namespace previously
		if targetNS == "" {
			logger.V(1).Info("PHPMYADMIN_NAMESPACE not set; skipping global deletion")
			return nil
		}

		// delete ingress, service, deployment (best-effort)
		ing := &networkingv1.Ingress{}
		_ = r.Get(ctx, types.NamespacedName{Name: "phpmyadmin", Namespace: targetNS}, ing)
		_ = r.Delete(ctx, ing)
		svc := &corev1.Service{}
		_ = r.Get(ctx, types.NamespacedName{Name: "phpmyadmin", Namespace: targetNS}, svc)
		_ = r.Delete(ctx, svc)
		dep := &appsv1.Deployment{}
		_ = r.Get(ctx, types.NamespacedName{Name: "phpmyadmin", Namespace: targetNS}, dep)
		_ = r.Delete(ctx, dep)
		logger.Info("requested deletion of global phpMyAdmin resources", "namespace", targetNS)
	}

	return nil
}
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
			"k8s.io/apimachinery/pkg/util/intstr"
			"os"
			"sigs.k8s.io/controller-runtime/pkg/client"
			ingress.Spec.TLS = []networkingv1.IngressTLS{{
				Hosts:      []string{host},
				SecretName: GetTLSSecretName(wp.Name),
		func ReconcilePHPMyAdmin(ctx context.Context, r client.Client, wp *crmv1.WordPressSite) error {
			logger := log.FromContext(ctx).WithValues("component", "phpmyadmin")

			// Global phpMyAdmin mode can be controlled by env var. Default: enabled.
			if strings.ToLower(os.Getenv("PHPMYADMIN_ENABLED")) == "false" {
				logger.V(1).Info("Global phpMyAdmin disabled via PHPMYADMIN_ENABLED")
				return nil
			}

			// Determine target namespace for the global phpMyAdmin resources. If not
			// provided via env, default to the namespace of the reconciled WordPressSite.
			targetNS := os.Getenv("PHPMYADMIN_NAMESPACE")
			if targetNS == "" {
				targetNS = wp.Namespace
			}

			// Determine which MariaDB namespace to point phpMyAdmin at. If not set,
			// assume the MariaDB cluster lives in the same namespace as the site.
			mariadbNS := os.Getenv("PHPMYADMIN_MARIADB_NAMESPACE")
			if mariadbNS == "" {
				mariadbNS = wp.Namespace
			}

			logger = logger.WithValues("phpmyadmin-namespace", targetNS, "mariadb-namespace", mariadbNS)

			// Prepare common metadata
			deploymentName := "phpmyadmin"
			serviceName := "phpmyadmin"
			ingressName := "phpmyadmin"

			// Ensure Deployment exists in the target namespace
			deployment := &appsv1.Deployment{}
			if err := r.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: targetNS}, deployment); err != nil {
				if errors.IsNotFound(err) {
					// Create a new Deployment for the shared phpMyAdmin instance
					envVars := []corev1.EnvVar{{Name: "PMA_HOST", Value: fmt.Sprintf("%s.%s.svc.cluster.local", MariaDBClusterName, mariadbNS)}}

					replicas := int32(1)
					labels := map[string]string{"app.kubernetes.io/managed-by": "kubepress-operator", "app.kubernetes.io/part-of": "kubepress", "app.kubernetes.io/name": "phpmyadmin-server"}

					deployment = &appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{Name: deploymentName, Namespace: targetNS, Labels: labels},
						Spec: appsv1.DeploymentSpec{
							Replicas: &replicas,
							Selector: &metav1.LabelSelector{MatchLabels: labels},
							Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "phpmyadmin", Image: os.Getenv("PHPMYADMIN_IMAGE"), Env: envVars, Ports: []corev1.ContainerPort{{Name: "apache", ContainerPort: 80}}}}}},
						},
					}

					if deployment.Spec.Template.Spec.Containers[0].Image == "" {
						// Fallback to default image if chart hasn't set PHPMYADMIN_IMAGE
						deployment.Spec.Template.Spec.Containers[0].Image = "phpmyadmin:latest"
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

			// Ensure Service exists
			svc := &corev1.Service{}
			if err := r.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: targetNS}, svc); err != nil {
				if errors.IsNotFound(err) {
					svc = &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: targetNS, Labels: map[string]string{"app.kubernetes.io/managed-by": "kubepress-operator", "app.kubernetes.io/part-of": "kubepress", "app.kubernetes.io/name": "phpmyadmin-service"}}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app.kubernetes.io/name": "phpmyadmin-server"}, Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(80)}}, Type: corev1.ServiceTypeClusterIP}}

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

			// Ensure Ingress exists using the global domain from env
			ingress := &networkingv1.Ingress{}
			if err := r.Get(ctx, types.NamespacedName{Name: ingressName, Namespace: targetNS}, ingress); err != nil {
				if errors.IsNotFound(err) {
					host := os.Getenv("PHPMYADMIN_DOMAIN")
					ingressClass := os.Getenv("PHPMYADMIN_INGRESS_CLASS") // optional override
					annotations := map[string]string{}
					// default nginx annotations only when class explicitly nginx
					if ingressClass == "nginx" {
						annotations["nginx.ingress.kubernetes.io/proxy-body-size"] = "32M"
						annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "100"
						annotations["nginx.ingress.kubernetes.io/proxy-send-timeout"] = "100"
					}
					// allow cluster-wide TLS issuer
					if issuer := os.Getenv("TLS_CLUSTER_ISSUER"); issuer != "" {
						annotations["cert-manager.io/cluster-issuer"] = issuer
					}

					ingress = &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: ingressName, Namespace: targetNS, Labels: map[string]string{"app.kubernetes.io/managed-by": "kubepress-operator", "app.kubernetes.io/part-of": "kubepress", "app.kubernetes.io/name": "phpmyadmin-ingress"}, Annotations: annotations}, Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: host, IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: func() *networkingv1.PathType { pt := networkingv1.PathTypePrefix; return &pt }(), Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: serviceName, Port: networkingv1.ServiceBackendPort{Number: 80}}}}}}}}}}

					if ingressClass != "" {
						ingress.Spec.IngressClassName = &ingressClass
					}

					// Configure TLS using consistent secret name
					if os.Getenv("TLS_CLUSTER_ISSUER") != "" || os.Getenv("PHPMYADMIN_TLS") == "true" {
						ingress.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{os.Getenv("PHPMYADMIN_DOMAIN")}, SecretName: GetTLSSecretName("phpmyadmin")}}
					}

					if err := r.Create(ctx, ingress); err != nil {
						logger.Error(err, "Failed to create phpMyAdmin ingress")
						return fmt.Errorf("failed to create phpMyAdmin ingress: %w", err)
					}
					logger.Info("Created phpMyAdmin Ingress", "name", ingressName, "namespace", targetNS)
				} else {
					logger.Error(err, "Failed to get phpMyAdmin Ingress")
					return fmt.Errorf("failed to get phpMyAdmin ingress: %w", err)
				}
			}

			return nil
		}
