# User Guide

Welcome to the User Guide! This document provides comprehensive instructions to help you effectively use KubePress.

As explained in the Quick Start, deploying a WordPress instance with KubePress involves two steps:
1. Create a Secret containing the WordPress credentials (These will be used as the intial username/password for WordPress, SFTP and the Database user).
2. Create a WordPress Custom Resource that references the Secret created in step 1.

### Create a Secret for WordPress Credentials

To create a WordPress instance, you need to create a secret first.

A fully functional Secret for a WordPress Instance looks like this:

```yaml
apiVersion: v1  
data:
  database: d3AtLWhvc3R6ZXJvLS13MS1jb20=
  databaseHost: a3ViZXByZXNzLmt1YmVwcmVzcy5zdmMuY2x1c3Rlci5sb2NhbA==
  databaseUsername: aG9zdHplcm8tYWRtaW4tYWIyOXNr
  databasePassword: cGFzc3dvcmQK
  password: cGFzc3dvcmQK
  username: YWRtaW4=
kind: Secret
metadata:
  name: wp--hostzero--w1-com
  namespace: kubepress
type: Opaque
```

The secret must contain the following keys. These are required in any case:
- `username`
- `password`

Optionally, you can also provide:
- `databaseUsername`
    - If this field is provided in the secret, it will be used to create the database user. If not provided, the value from `username` will be used with a random suffix appended to it. The random suffix is to ensure uniqueness of the database user, because all database users will be created in the same database cluster.
- `databasePassword`
    - If this field is provided in the secret, it will be used to create the database user. If not provided, a random password will be generated for the database user. This is optionally independent of the `databaseUsername` field.

If you set the `wp.Spec.Database.CreateNew` to `false`, you must provide the following additional keys in the secret (in addition to `username` and `password`):
- `database`
    - The name of the existing database to use.
- `databaseHost`
    - The host of the existing database to use (and connect to).
- `databaseUsername`
    - The username of the existing database user to use.
- `databasePassword`
    - The password of the existing database user to use.

If you set the `wp.Spec.Database.CreateNew` to `true` (which is the default), you do not need to provide the three additional keys from above. The operator will create a new database and database user for you.

This data will be used to create:
- the SFTP user
- the Database user
- the initial WordPress admin user
- (and also indirectly the access to PHPMyAdmin, because you will need the Database user to access PHPMyAdmin)

After the initial creation of the WordPress instance, an update of username/password in the secret will not update:
- the Admin user in WordPress
- the password in the Database
    - However, you can set a label to the secret, and the MariaDB operator will pick up the change and update the password in the database. According to the [MariaDB Operator documentation](https://github.com/mariadb-operator/mariadb-operator/blob/main/docs/api_reference.md#userspec), you can set the label `k8s.mariadb.com/watch` on the secret, and the operator will watch for changes.

The newly updated credentials will be used in the deployment after a restart of the pods.

### Create a WordPress Custom Resource

After creating the secret, you can create a WordPress Custom Resource that references the secret.

A fully functional WordPress Custom Resource looks like this:

```yaml
apiVersion: crm.hostzero.de/v1
kind: WordPressSite
metadata:
  name: wp--w2-com
  namespace: kubepress
spec:
  adminEmail: admin@example.com
  adminUserSecretKeyRef: wp--w2-com
  database:
    createNew: true
  ingress:
    host: w2.com
    tls: false
  siteTitle: w2
  wordpress:
    image: wordpress:latest
    maxUploadLimit: 64M
    replicas: 1
    resources:
      cpuLimit: 500m
      cpuRequest: 250m
      memoryLimit: 1Gi
      memoryRequest: 512Mi
    storageSize: 10Gi
```

### Disabling TLS for Ingress

If you're using Cloudflare Argo Tunnel or another reverse proxy that handles TLS termination, you should disable TLS on the Kubernetes ingress to avoid certificate conflicts. Simply set the `ingress.tls` field to `false`:

```yaml
apiVersion: crm.hostzero.de/v1
kind: WordPressSite
metadata:
  name: wordpress-tunnel
spec:
  # ... other fields ...
  ingress:
    host: wordpress.example.com
    tls: false  # Disable TLS for external termination (e.g., Cloudflare Argo Tunnel)
```

When `tls` is set to `false`:
- No TLS configuration will be added to the ingress
- The ingress will serve HTTP traffic only
- External services like Cloudflare Argo Tunnel can handle TLS termination

### Custom Ingress Controller and Annotations

By default, KubePress creates Ingress resources for the NGINX controller. You can customize this by specifying a different IngressClass and adding custom annotations.

#### Using a Different Ingress Controller

To use a different ingress controller (e.g., Traefik, Istio), specify the `ingressClassName`:

```yaml
apiVersion: crm.hostzero.de/v1
kind: WordPressSite
metadata:
  name: wordpress-traefik
spec:
  # ... other fields ...
  ingress:
    host: wordpress.example.com
    tls: false
    ingressClassName: traefik  # Use Traefik instead of NGINX
```

#### Adding Custom Annotations

You can add custom annotations to the Ingress resource for controller-specific configurations. Custom annotations are merged with the default NGINX annotations:

```yaml
apiVersion: crm.hostzero.de/v1
kind: WordPressSite
metadata:
  name: wordpress-custom
spec:
  # ... other fields ...
  ingress:
    host: wordpress.example.com
    tls: false
    ingressClassName: nginx
    annotations:
      cert-manager.io/cluster-issuer: "letsencrypt-prod"
      nginx.ingress.kubernetes.io/ssl-redirect: "true"
      nginx.ingress.kubernetes.io/force-ssl-redirect: "true"
```

#### Traefik-Specific Example

When using Traefik, you can configure it with custom annotations:

```yaml
apiVersion: crm.hostzero.de/v1
kind: WordPressSite
metadata:
  name: wordpress-traefik-secure
spec:
  siteTitle: My WordPress Site
  adminEmail: admin@example.com
  adminUserSecretKeyRef: wordpress-secret
  database:
    createNew: true
  wordpress:
    image: wordpress:latest
    storageSize: 10Gi
  ingress:
    host: wp.example.com
    tls: true
    ingressClassName: traefik
    annotations:
      traefik.ingress.kubernetes.io/router.entrypoints: "websecure"
      traefik.ingress.kubernetes.io/router.middlewares: "security@kubecontext"
```

### Accessing phpMyAdmin

When phpMyAdmin is enabled (which is the default), you can access it at the configured domain (set via `PHPMYADMIN_DOMAIN` environment variable). To log in:

1. Use the database credentials from your WordPress secret:
   - **Username**: The value of `databaseUsername` field in the secret
   - **Password**: The value of `databasePassword` field in the secret
   - **Server**: The MariaDB cluster host (automatically configured)

2. Select your WordPress database from the left sidebar to manage tables, run queries, etc.

**Note**: phpMyAdmin is configured to connect to the MariaDB cluster automatically. The login credentials are the database user credentials created for your WordPress site.

### Disabling Automatic phpMyAdmin Deployment

By default, KubePress automatically deploys a phpMyAdmin instance for each WordPress site. If you do not want phpMyAdmin to be deployed for a specific site, you can disable it by setting the `disablePhpMyAdmin` field to `true` in your WordPressSite spec:

```yaml
apiVersion: crm.hostzero.de/v1
kind: WordPressSite
metadata:
  name: wordpress-no-phpmyadmin
spec:
  # ... other fields ...
  disablePhpMyAdmin: true  # This prevents automatic phpMyAdmin deployment for this site
  ingress:
    host: example.com
    tls: false
```

When `disablePhpMyAdmin` is set to `true`:
- No phpMyAdmin Deployment or Service will be created for this WordPress site
- You can still access the database using your own tools or a shared phpMyAdmin instance if available

**Global phpMyAdmin Configuration (Environment Variables)**

- The operator manages a single, global phpMyAdmin instance per target namespace. The behavior and placement of that instance can be controlled using the following environment variables set for the operator process (for example in the deployment or Helm chart `values.yaml`):

- `PHPMYADMIN_ENABLED` : if set to `false` (case-insensitive) the operator will skip creating phpMyAdmin. When explicitly `false`, the operator's `DeletePHPMyAdmin` routine will perform a best-effort deletion of the global phpMyAdmin resources in `PHPMYADMIN_NAMESPACE`.
- `PHPMYADMIN_NAMESPACE` : target namespace to create the global phpMyAdmin `Deployment`, `Service` and `Ingress`. If unset, the operator uses the `kubepress` namespace as the default.
- `PHPMYADMIN_MARIADB_NAMESPACE` : namespace hosting the MariaDB cluster used by phpMyAdmin. Defaults to the `kubepress` namespace if unset.
- `PHPMYADMIN_IMAGE` : container image for phpMyAdmin. Default: `phpmyadmin:latest`.
- `PHPMYADMIN_DOMAIN` : hostname for the phpMyAdmin `Ingress`. If empty, ingress creation is skipped.
- `PHPMYADMIN_INGRESS_CLASS` : optional `IngressClassName` to set on the phpMyAdmin ingress. If set to `nginx`, a set of common nginx-specific annotations (proxy body size, timeouts) are added.
- `PHPMYADMIN_TLS` : if set to `true` (string) the operator will add a TLS entry to the phpMyAdmin Ingress and use the secret returned by `GetTLSSecretName("phpmyadmin")`.
- `TLS_CLUSTER_ISSUER` : if set, the operator adds the `cert-manager.io/cluster-issuer` annotation to the Ingress and will enable TLS for the host.

Examples for deploying the operator with the phpMyAdmin domain set are present in `local-values.yaml` and `dist/chart/values.yaml` (see `PHPMYADMIN_DOMAIN`).

For more information about the fields in the WordPress Custom Resource, please look directly at the [wordpresssite_types.go](../api/v1/wordpresssite_types.go) file in the `api/v1` directory.