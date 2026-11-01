resource "stackhero_redis" "example" {
  stack_id = stackhero_stack.example.id
  instance = "1G"
  region   = "europe"

  configuration_json = jsonencode({
    tlsOnly         = true
    maxMemoryPolicy = "noeviction"
  })
}