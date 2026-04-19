# 1. List current crontab and save to temporary file
crontab -l > mycron

# 2. Add new job (using grep to prevent duplicates)
# (crontab -l 2>/dev/null; echo "0 2 * * * cd ${HOME}/open-democracy && ./open-democracy-crawler >> /var/log/open-democracy-crawler.log 2>&1") | crontab -
echo "0 2 * * * cd ${HOME}/open-democracy && ./open-democracy-crawler >> /var/log/open-democracy-crawler.log 2>&1" | grep -Fvf - mycron || echo "0 2 * * * cd ${HOME}/open-democracy && ./open-democracy-crawler >> /var/log/open-democracy-crawler.log 2>&1" >> mycron

# 3. (Optional) Sort and remove any accidental duplicates
sort -u mycron -o mycron

# 4. Install the new crontab
crontab mycron

# 5. Remove temporary file
rm mycron
