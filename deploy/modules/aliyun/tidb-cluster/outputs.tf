output "tidb_hostname" {
  value = module.tidb-cluster.tidb_hostname
}

output "monitor_hostname" {
  value = module.tidb-cluster.monitor_hostname
}

output "tidb_endpoint_dev" {
  value = module.tidb-cluster-develop.tidb_endpoint
}

output "monitor_endpoint_dev" {
  value = module.tidb-cluster-develop.monitor_endpoint
}