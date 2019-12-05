output "region" {
  value = var.ALICLOUD_REGION
}

output "cluster_id" {
  value = module.tidb-operator.cluster_id
}

output "kubeconfig_file" {
  value = module.tidb-operator.kubeconfig_filename
}

output "vpc_id" {
  value = module.tidb-operator.vpc_id
}

output "ssh_key_file" {
  value = local.key_file
}

output "tidb_version" {
  value = var.tidb_version
}

output "tidb_endpoint_dev" {
  value = module.tidb-cluster-develop.tidb_endpoint
}

output "monitor_endpoint_dev" {
  value = module.tidb-cluster-develop.monitor_endpoint
}

