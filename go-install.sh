#https://github.com/Jrohy/go-install
source <(curl -L https://go-install.netlify.app/install.sh)
go mod tidy -compat=1.18
go mod download
