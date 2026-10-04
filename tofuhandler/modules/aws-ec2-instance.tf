# aws.ec2-instance: the app's EC2 host with its Elastic IP, and the instance
# role and instance profile that let it pull the app's image, read the app's
# secrets and take commands over SSM. Taken from aws-app-infra's perapp
# module (ec2.tf, eip.tf and the instance half of iam.tf), with the AMI
# passed in so planning needs no AWS lookup.

variable "name_prefix" {
  type = string
}

variable "app_name" {
  type = string
}

variable "instance_type" {
  type    = string
  default = "t3.small"
}

variable "ami_id" {
  type = string
}

variable "subnet_id" {
  type = string
}

variable "security_group_id" {
  type = string
}

variable "ecr_repository_arn" {
  type = string
}

variable "region" {
  type = string
}

variable "user_data" {
  type    = string
  default = ""
}

resource "aws_iam_role" "instance" {
  name = "${var.name_prefix}-instance"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

resource "aws_iam_role_policy_attachment" "instance_ssm" {
  role       = aws_iam_role.instance.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy" "instance_ecr" {
  name = "${var.name_prefix}-ecr-pull"
  role = aws_iam_role.instance.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      { Effect = "Allow", Action = ["ecr:GetAuthorizationToken"], Resource = "*" },
      { Effect = "Allow", Action = ["ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer", "ecr:BatchCheckLayerAvailability"], Resource = var.ecr_repository_arn },
    ]
  })
}

resource "aws_iam_role_policy" "instance_app_secrets" {
  name = "${var.name_prefix}-app-secrets"
  role = aws_iam_role.instance.id
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = ["ssm:GetParameter", "ssm:GetParametersByPath"], Resource = "arn:aws:ssm:${var.region}:*:parameter/mitstack-app/${var.app_name}/*" }]
  })
}

resource "aws_iam_instance_profile" "instance" {
  name = "${var.name_prefix}-instance"
  role = aws_iam_role.instance.name
}

resource "aws_instance" "this" {
  ami                         = var.ami_id
  instance_type               = var.instance_type
  subnet_id                   = var.subnet_id
  vpc_security_group_ids      = [var.security_group_id]
  iam_instance_profile        = aws_iam_instance_profile.instance.name
  associate_public_ip_address = true
  user_data_base64            = var.user_data == "" ? null : base64encode(var.user_data)
  user_data_replace_on_change = false

  root_block_device {
    volume_type           = "gp3"
    volume_size           = 20
    encrypted             = true
    delete_on_termination = true
  }

  metadata_options {
    http_tokens   = "required"
    http_endpoint = "enabled"
  }

  lifecycle {
    ignore_changes = [ami, user_data_base64]
  }

  depends_on = [aws_iam_role_policy_attachment.instance_ssm]
}

resource "aws_eip" "this" {
  domain = "vpc"
}

resource "aws_eip_association" "this" {
  instance_id   = aws_instance.this.id
  allocation_id = aws_eip.this.id
}

output "hostId" {
  value = aws_instance.this.id
}

output "execKind" {
  value = "aws-ssm"
}

output "execTarget" {
  value = aws_instance.this.id
}

output "publicIp" {
  value = aws_eip.this.public_ip
}

output "host_type" {
  value = aws_instance.this.instance_type
}

output "cloud_region" {
  value = var.region
}
