terraform {
  required_providers {
    stackhero = {
      source  = "avaly/stackhero"
      version = "~> 1.0.0"
    }
  }
}

provider "stackhero" {
  cli_path = "stackhero"
}
