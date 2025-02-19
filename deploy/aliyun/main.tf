variable "ALICLOUD_ACCESS_KEY" {
}

variable "ALICLOUD_SECRET_KEY" {
}

variable "ALICLOUD_REGION" {
}

provider "alicloud" {
  region     = var.ALICLOUD_REGION
  access_key = var.ALICLOUD_ACCESS_KEY
  secret_key = var.ALICLOUD_SECRET_KEY
}

locals {
  credential_path = "${path.cwd}/credentials"
  kubeconfig      = "${local.credential_path}/kubeconfig"
  key_file        = "${local.credential_path}/${var.cluster_name}-key.pem"
}

// AliCloud resource requires path existing
resource "null_resource" "prepare-dir" {
  provisioner "local-exec" {
    command = "mkdir -p ${local.credential_path}"
  }
}

module "tidb-operator" {
  source = "../modules/aliyun/tidb-operator"

  region                        = var.ALICLOUD_REGION
  access_key                    = var.ALICLOUD_ACCESS_KEY
  secret_key                    = var.ALICLOUD_SECRET_KEY
  cluster_name                  = var.cluster_name
  operator_version              = var.operator_version
  operator_helm_values          = var.operator_helm_values == "" ? "" : file(var.operator_helm_values)
  k8s_pod_cidr                  = var.k8s_pod_cidr
  k8s_service_cidr              = var.k8s_service_cidr
  vpc_cidr                      = var.vpc_cidr
  vpc_id                        = "vpc-2ze6o5gjm6hj9p8r4ji45"
  default_worker_cpu_core_count = var.default_worker_core_count
  group_id                      = "sg-2ze7rjmq1tkuinu89op8"
  key_file                      = local.key_file
  kubeconfig_file               = local.kubeconfig
}

provider "helm" {
  alias          = "default"
  insecure       = true
  install_tiller = false
  kubernetes {
    config_path = module.tidb-operator.kubeconfig_filename
  }
}

module "tidb-cluster-develop" {
  source = "../modules/aliyun/tidb-cluster"
  providers = {
    helm = helm.default
  }

  cluster_name = "develop-cluster"
  ack          = module.tidb-operator

  tidb_version               = var.tidb_version
  tidb_cluster_chart_version = var.tidb_cluster_chart_version
  pd_instance_type           = "ecs.c6.xlarge" //4 vCPU + 8 GiB
  pd_count                   = 1
  tikv_instance_type         = "ecs.g6.2xlarge" //8 vCPU + 32 GiB
  tikv_count                 = 3
  tidb_instance_type         = "ecs.c6.2xlarge"  //8 vCPU + 16 GiB
  tidb_count                 = 1
  monitor_instance_type      = "ecs.c5.xlarge" //4 vCPU + 8 GiB
  override_values            = file("develop-cluster.yaml")
}

//module "tidb-cluster-product" {
//  source = "../modules/aliyun/tidb-cluster"
//  providers = {
//    helm = helm.default
//  }
//
//  cluster_name = "product-cluster"
//  ack          = module.tidb-operator
//
//  tidb_version               = var.tidb_version
//  tidb_cluster_chart_version = var.tidb_cluster_chart_version
//  pd_instance_type           = "ecs.c6.2xlarge" //8 vCPU + 16 GiB
//  pd_count                   = 3
//  tikv_instance_type         = "ecs.c6.4xlarge" // 16 vCPU + 32 GiB
//  tikv_count                 = 3
//  tidb_instance_type         = "ecs.c6.4xlarge" // 16 vCPU + 32 GiB
//  tidb_count                 = 2
//  monitor_instance_type      = "ecs.c6.2xlarge" //8 vCPU + 16 GiB
//  override_values            = file("product-cluster.yaml")
//}
