pid=$(lsof -ti:8080) && [ -n "$pid" ] && kill "$pid"
cd open-democracy
cp open-democracy.d* ../
git pull
cp ../open-democracy.d* .
./scripts/install.sh
make build
screen -dmS web make server
