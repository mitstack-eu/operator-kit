# aws.egress-proxy: a t3.nano squid proxy in the host's subnet with its own
# public address, security group, IAM role and instance profile, configured
# over SSM. Taken from aws-app-infra's perapp module (egress.tf and the
# proxy's document and association in ssm.tf). The squid script is a content
# input, as perapp renders it from templates/egress_proxy.sh.tftpl.
#
# The rule that lets the app's security group reach the proxy is written onto
# that other resource's group, so this type changes an object another type
# owns.

variable "name_prefix" {
  type = string
}

variable "ami_id" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "subnet_id" {
  type = string
}

variable "app_security_group_id" {
  type = string
}

variable "proxy_script" {
  type = string
}

resource "aws_security_group" "proxy" {
  name        = "${var.name_prefix}-proxy"
  description = "Per-app egress proxy: squid on 3128 for the app box only, HTTPS out."
  vpc_id      = var.vpc_id

  ingress {
    description     = "Squid, from the app box only"
    from_port       = 3128
    to_port         = 3128
    protocol        = "tcp"
    security_groups = [var.app_security_group_id]
  }

  egress {
    description = "HTTPS to the allowlisted destinations squid permits"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_vpc_security_group_egress_rule" "app_to_proxy" {
  security_group_id            = var.app_security_group_id
  description                  = "The app box reaches the internet only through its egress proxy"
  ip_protocol                  = "tcp"
  from_port                    = 3128
  to_port                      = 3128
  referenced_security_group_id = aws_security_group.proxy.id
}

resource "aws_iam_role" "proxy" {
  name = "${var.name_prefix}-proxy"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "ec2.amazonaws.com" }, Action = "sts:AssumeRole" }]
  })
}

resource "aws_iam_role_policy_attachment" "proxy_ssm" {
  role       = aws_iam_role.proxy.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "proxy" {
  name = "${var.name_prefix}-proxy"
  role = aws_iam_role.proxy.name
}

resource "aws_instance" "proxy" {
  ami                         = var.ami_id
  instance_type               = "t3.nano"
  subnet_id                   = var.subnet_id
  associate_public_ip_address = true
  vpc_security_group_ids      = [aws_security_group.proxy.id]
  iam_instance_profile        = aws_iam_instance_profile.proxy.name

  metadata_options {
    http_tokens   = "required"
    http_endpoint = "enabled"
  }

  root_block_device {
    volume_type           = "gp3"
    volume_size           = 8
    encrypted             = true
    delete_on_termination = true
  }

  lifecycle {
    ignore_changes = [ami]
  }

  depends_on = [aws_iam_role_policy_attachment.proxy_ssm]
}

resource "aws_ssm_document" "proxy" {
  name          = "${var.name_prefix}-egress-proxy"
  document_type = "Command"
  content = jsonencode({
    schemaVersion = "2.2"
    description   = "Install and configure squid on the egress proxy"
    mainSteps = [{
      action = "aws:runShellScript"
      name   = "configureSquid"
      inputs = { runCommand = split("\n", var.proxy_script) }
    }]
  })
}

resource "aws_ssm_association" "proxy" {
  name = aws_ssm_document.proxy.name
  targets {
    key    = "InstanceIds"
    values = [aws_instance.proxy.id]
  }
  wait_for_success_timeout_seconds = 600
}
