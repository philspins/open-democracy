I'd done some testing of this both locally and deployed to the server, using this for my testing method:
1. Browse to Members of Parliament page
2. Select Provincial level
3. Select a Province (I worked through each Province in the list)
4. Click Search
5. Click on 3-5 random MPPs that come back
6. Check to see if the MPPs photo is displayed correctly
7. Check to see if the MPP has data in the Voting by Category section
8. Check to see if the MPP has data in the Recent Votes section

These are my findings:
- ON: fully functional for some MPPs, others missing Voting by Category (example problem slug: /members/ontario-legislature-amarjot-sandhu)
- NB: fully functional for some MPPs, others missing Voting by Category and Recent Votes (example problem slug: /members/nb-legislature-wilson-sherry)
- BC: fully functional for some MPPs, others missing Voting by Category (example problem slug: /members/bc-legislature-christine-boyle)
- AB: Photos work; no data in Voting by Category or Recent Votes data (example problem slug: `/members/alberta-legislature-member-information?mid=0924&legl=31&from=mla_home`)
- MB: Photos not showing on localhost, but are showing on open-democracy.ca; no data in Voting by Category or Recent Votes data (example problem slug: /members/manitoba-legislature-sala.html)
- SK: Photos not showing on localhost, but are showing on open-democracy.ca; no data in Voting by Category or Recent Votes data (example problem slug: /members/saskatchewan-legislature-member-details?first=Betty&last=Nippi-Albright)
- NL: No photo data, showing first name initial in photo frame; no data in Voting by Category or Recent Votes data (Example problem slug: /members/newfoundland-labrador-legislature-jeff-dwyer)
- NS: Photos not showing on localhost, but are showing on open-democracy.ca; no data in Voting by Category or Recent Votes data (example problem slug: /members/nova-scotia-legislature-adegoke-fadare)
- PE: photos work correctly; no data in Voting by Category or Recent Votes
- QC: photos work correctly; some members have data under Recent Votes, no data in Voting by Category (example MPP with no voting data /members/quebec-assemblee-nationale-zaga-mendez-alejandra-19263)
- SK: Photos not showing on localhost, but are showing on open-democracy.ca; no data in Voting by Category or Recent Votes

My task for you:
1. Please perform an analysis to determine what the problem is for each province, and create a detailed implementation plan that will fix each province. Use sub-agents to split up this work and execute in parralel.
2. For each province's implementation plan, please have each sub-agent work on the fixes for their province in a new branch.
3. After implementing fixes in each branch, the sub-agents should re-run the crawler against a brand new database.
4. Once the crawler is complete for each province, the sub-agent should run `make server` and then crawl the localhost `/members` page to perform the exact same set of test steps as I laid out in the beginning of this prompt (steps 1 though 8)
5. If the sub-agents do not come back with an answer of 'fully functional', have them repeat these tasks until the '/members/{mpp-name}' pages are fully functional for each province.