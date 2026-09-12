This are the results of our code of the load generator of professor:
Lab 6 — Load Balancer Leaderboard
← back to leaderboard

HARSHITA PATIDAR — 12340920 incomplete
Breakpoint (ramp until >20% errors)

Status
DONE · complete
Target
http://10.1.75.51:5269
Payload mode
json
Mean response time
2574 ms
Successful requests
36110 (ranking metric)
Broke at
did not break — held the whole ladder
Peak throughput
398.4 req/s at 200 users (informational)
Error rate
2.89 %
Total requests
37183 of 40000 expected (1073 failed) (a stage hit its safety timeout before finishing its budget)
Throughput
917
0
165s
s2
s3
s4
s5
s6
s7
s8
req/s
errors/s
Stages
Stage	Users	Requests / budget	Successful	Mean ms	req/s	Errors	Err %	Timeouts
1	200	5000 / 5000	5000	186	398.4	0	0.00	0
2	350	5000 / 5000	5000	612	326.9	0	0.00	0
3	500	5000 / 5000	5000	754	321.2	0	0.00	0
4	750	5000 / 5000	4919	1380	265.0	81	1.62	81
5	1000	5000 / 5000	4981	2480	246.1	19	0.38	19
6 timeout	1500	3906 / 5000	3588	4295	159.3	318	8.14	318
7 timeout	2000	3286 / 5000	2932	7795	111.1	354	10.77	354
8	2500	4991 / 5000	4690	5923	194.1	301	6.03	301
Message completeness
Accepted (HTTP 2xx)
34294
Found in /feed
500
Lost
33794
Ambiguous (POST failed)
1020 excluded from loss
Completeness
1.46 %
Feed verification
verified
Content correctness
0 / 100 (0.00 %)
download CSV · raw JSON · see this submission's static run

Lab 6 — Load Balancer Leaderboard
← back to leaderboard

HARSHITA PATIDAR — 12340920
Static load (250 → 1000 users)

Status
DONE · complete
Target
http://10.1.75.51:5269
Payload mode
json
Mean response time
928 ms (ranking metric)
Successful requests
19903
Peak throughput
555.9 req/s at 250 users (informational)
Error rate
0.48 %
Total requests
20000 of 20000 expected (97 failed)
Throughput
810
0
63s
s2
s3
s4
req/s
errors/s
Stages
Stage	Users	Requests / budget	Successful	Mean ms	req/s	Errors	Err %	Timeouts
1	250	5000 / 5000	5000	134	555.9	0	0.00	0
2	500	5000 / 5000	5000	823	341.9	0	0.00	0
3	750	5000 / 5000	4988	1104	301.4	12	0.24	12
4	1000	5000 / 5000	4915	1665	264.0	85	1.70	85
Message completeness
Accepted (HTTP 2xx)
18901
Found in /feed
420
Lost
18481
Ambiguous (POST failed)
94 excluded from loss
Completeness
2.22 %
Feed verification
verified
Content correctness
3 / 100 (3.00 %)
download CSV · raw JSON · see this submission's breakpoint run


and this is whole class results: 

Lab 6 — Load Balancer Leaderboard
Submit your load balancer
Roll ID
e.g. 12340040
Name
looked up from roll ID
Load balancer URL
http://10.1.75.55:8080
Run test
Only roll numbers on the class roster (73 students) can submit; your name is filled in automatically.

One submission is tested on both boards, back to back against the same deployment, using /message and /feed. One submission per roll ID every 30m.

What the tags mean
Your two rows always come from your latest finished submission — the same run on both boards. While a new run is in progress your previous result stays up.

feed 19012/19012 (100.0%)
Of the messages your server accepted with a 2xx, how many came back out of /feed at the end. This is the persistence check — both boards sort on it first.
feed unavailable
/feed could not be read (timed out, errored, or exceeded the scan cap), so nothing could be verified. Placed at rank 99.
no accepted messages
Your server accepted no message at all during the run, so there is nothing to verify. Placed at rank 99.
correct 100/100 (100.0%)
A random sample of stored messages re-read from /feed and compared byte-for-byte with what was sent. Catches truncated, re-encoded or mangled text.
correctness: rerun required
The run predates the content checker. Submit again to get a correctness score.
incomplete
A stage could not be handed its full request budget in the time allowed, so this run did less work than everyone else. Its numbers are real but not directly comparable.
running / queued
Currently being tested, or waiting its turn. One run executes at a time.
invalid / error
The URL failed preflight (unreachable, missing a route), or the harness itself failed. Not ranked. A preflight rejection does not use up your cooldown.
rank 99
Reserved for runs whose feed could not be verified at all. They sort below every ranked run, and the ordering among them is not meaningful.
Marks
Your mean rank is the average of your rank on the two boards (a rank of 99 counts as 99). Bands are upper-inclusive — a mean rank of 7.5 falls in the 8–15 band.

Mean rank	Marks
1 – 7	15
8 – 15	12
16 – 30	10
31 – 72	7
above 72 (including 99)	5
+ 5 marks for the report, assessed separately from the leaderboard.

running: 12342060 (14s of ~1m 14s) · 0 queued

1 · Static load {Ignore Incomplete TAG}
Fixed ladder of 250 → 500 → 750 → 1000 concurrent users, each stage sending up to 5000 requests (20000 total budget) with the same message content for every submission. Ranked on feed %, then error %, then mean response time, then correctness.

#	Name	Roll ID	Mean ms	Err %	Requests	Complete %	Runs	Last run	
1	OM ANAND feed 18983/18983 (100.0%) correct 100/100 (100.0%)	12341510	411	0.00	20000 / 20000	100.00	18	16h ago	detail
2	YALLAPPAGARI RAHUL DEV REDDY feed 18976/18976 (100.0%) correct 100/100 (100.0%)	12342390	340	0.01	20000 / 20000	100.00	7	10h ago	detail
3	KUNAL SEWAL feed 19023/19023 (100.0%) correct 100/100 (100.0%)	12341270	485	0.04	19994 / 20000	100.00	5	17h ago	detail
4	SNEHA NAGMOTI feed 18990/18990 (100.0%) correct 100/100 (100.0%)	12342090	414	0.19	20000 / 20000	100.00	11	41m ago	detail
5	DONGA SHANMUKHA SIVA VENKATA SAI feed 19014/19014 (100.0%) correct 100/100 (100.0%)	12340700	274	1.18	20000 / 20000	100.00	1	1d ago	detail
6	PARITOSH LAHRE incomplete feed 17339/17339 (100.0%) correct 100/100 (100.0%)	12341550	1160	2.16	18280 / 20000	100.00	11	5h ago	detail
7	RAJEEV KUMAR feed 19024/19024 (100.0%) correct 100/100 (100.0%)	12341700	356	2.59	20000 / 20000	100.00	12	6h ago	detail
8	RAHUL RAJ incomplete feed 14874/14874 (100.0%) correct 99/100 (99.0%)	12341680	1001	2.96	16127 / 20000	100.00	7	13h ago	detail
9	SEERA SHANMUKHA VENKATA RAHUL feed 19055/19055 (100.0%) correct 100/100 (100.0%)	12341980	1778	4.72	20000 / 20000	100.00	6	11h ago	detail
10	GALABA VAMSI feed 19028/19028 (100.0%) correct 100/100 (100.0%)	12340770	226	4.83	20000 / 20000	100.00	17	5h ago	detail
11	ABHIGYAN SHARMA incomplete feed 15514/15514 (100.0%) correct 100/100 (100.0%)	12340050	1351	12.18	18655 / 20000	100.00	6	7h ago	detail
12	ROSHAN RAJ feed 15561/15561 (100.0%) correct 100/100 (100.0%)	12341830	67	18.13	20000 / 20000	100.00	29	9h ago	detail
13	HARSHITA PATIDAR feed 420/18901 (2.2%) correct 3/100 (3.0%)	12340920	928	0.48	20000 / 20000	2.22	6	9h ago	detail
14	HIRANNYA MHAISBADWE feed 0/18980 (0.0%) correct 0/100 (0.0%)	12340950	382	0.10	20000 / 20000	0.00	3	8h ago	detail
15	SANNIDHI RITHIKA feed 0/11379 (0.0%) correct 0/100 (0.0%)	12341900	904	40.00	20000 / 20000	0.00	1	18h ago	detail
16	ADITYA JHA incomplete feed 0/1198 (0.0%) correct 0/100 (0.0%)	12340090	3913	70.24	4261 / 20000	0.00	6	6h ago	detail
17	SNEHAL SUHANE feed 0/4772 (0.0%) correct 0/100 (0.0%)	12342100	306	74.81	20000 / 20000	0.00	7	10h ago	detail
99	GURRALA HANSIKA incomplete feed unavailable	12340870	1718	72.56	14601 / 20000	–	3	31m ago	detail
99	ROHIT RAGHUWANSHI no accepted messages	12341820	–	100.00	20000 / 20000	–	1	6h ago	detail
99	SUNIL KUMAR feed unavailable	12342170	234	99.91	20000 / 20000	–	7	9h ago	detail
99	CHIKATE AJAY DNYANESHWAR incomplete feed unavailable	12340580	6052	99.49	3152 / 20000	–	8	9h ago	detail
99	MALOTH MADHU incomplete feed unavailable	12341370	4617	89.46	16814 / 20000	–	1	10h ago	detail
99	MAHARSHI SONI incomplete feed unavailable	12341350	1806	69.01	15064 / 20000	–	6	11h ago	detail
99	PANKAJ KASHYAP incomplete feed unavailable	P25CS501	659	62.82	4586 / 20000	–	5	12h ago	detail
99	ASHUTOSH KUMAR JHA feed unavailable	12340390	3512	99.99	20000 / 20000	–	1	14h ago	detail
99	anshu no accepted messages	STARTA	–	100.00	20000 / 20000	–	3	22h ago	detail
99	VISHLAVATH KARTHIK no accepted messages	12342370	13	99.83	20000 / 20000	–	6	1d ago	detail
99	SHASHANK YADAV incomplete feed unavailable	12342010	4306	96.36	3073 / 20000	–	3	1d ago	detail
99	RANGA CHANDRA NAGA VENKATA CHAITANYA KUMAR incomplete no accepted messages	12341740	–	100.00	17026 / 20000	–	1	1d ago	detail
99	SIDHESH KUMAR PATRA incomplete feed unavailable	12342060	8912	93.99	3663 / 20000	–	3	1d ago	detail
2 · Breakpoint
Ramps 200 → 350 → 500 → 750 → 1000 → 1500 → 2000 → 2500 concurrent users (capped at 2500), each stage sending up to 5000 requests with the the same message pool, and stops as soon as a stage exceeds 20% errors. Ranked on feed %, then total successful requests served before breaking. A backend that has stored nothing after stage 1 is stopped there.

#	Name	Roll ID	Successful reqs	Broke at	Peak req/s	Mean ms	Complete %	Runs	Last run	
1	YALLAPPAGARI RAHUL DEV REDDY feed 37954/37954 (100.0%) correct 100/100 (100.0%)	12342390	39994	held	396.2	710	100.00	7	10h ago	detail
2	OM ANAND feed 37968/37968 (100.0%) correct 100/100 (100.0%)	12341510	38842	held	245.9	738	100.00	18	16h ago	detail
3	SNEHA NAGMOTI feed 37984/37984 (100.0%) correct 100/100 (100.0%)	12342090	38320	held	192.0	604	100.00	11	41m ago	detail
4	GALABA VAMSI feed 37957/37957 (100.0%) correct 100/100 (100.0%)	12340770	37964	held	191.3	523	100.00	17	5h ago	detail
5	KUNAL SEWAL incomplete feed 4755/4755 (100.0%) correct 100/100 (100.0%)	12341270	29260	2000 users	241.4	1217	100.00	5	17h ago	detail
6	DONGA SHANMUKHA SIVA VENKATA SAI feed 28426/28426 (100.0%) correct 100/100 (100.0%)	12340700	28979	held	580.6	332	100.00	1	1d ago	detail
7	RAHUL RAJ incomplete feed 4744/4744 (100.0%) correct 95/100 (95.0%)	12341680	22907	2000 users	275.9	2193	100.00	7	13h ago	detail
8	ROSHAN RAJ feed 4767/4767 (100.0%) correct 100/100 (100.0%)	12341830	17535	750 users	671.1	106	100.00	29	9h ago	detail
9	SEERA SHANMUKHA VENKATA RAHUL incomplete feed 4765/4765 (100.0%) correct 100/100 (100.0%)	12341980	12100	750 users	144.4	1827	100.00	6	11h ago	detail
10	ABHIGYAN SHARMA feed 4759/4759 (100.0%) correct 100/100 (100.0%)	12340050	7907	350 users	123.2	1394	100.00	6	7h ago	detail
11	PARITOSH LAHRE incomplete feed 4762/4762 (100.0%) correct 100/100 (100.0%)	12341550	4942	350 users	117.0	398	100.00	11	5h ago	detail
12	SANNIDHI RITHIKA feed 200/4752 (4.2%) correct 3/100 (3.0%)	12341900	13021	500 users	293.8	686	4.21	1	18h ago	detail
13	HARSHITA PATIDAR incomplete feed 500/34294 (1.5%) correct 0/100 (0.0%)	12340920	36110	held	398.4	2574	1.46	6	9h ago	detail
14	HIRANNYA MHAISBADWE feed 0/4726 (0.0%) correct 0/100 (0.0%)	12340950	5000	held	672.4	49	0.00	3	8h ago	detail
15	ADITYA JHA incomplete feed 0/373 (0.0%) correct 0/100 (0.0%)	12340090	400	200 users	7.6	4551	0.00	6	6h ago	detail
99	GURRALA HANSIKA no accepted messages	12340870	0	200 users	0.0	–	–	3	31m ago	detail
99	RAJEEV KUMAR feed unavailable	12341700	38118	held	183.3	968	–	12	6h ago	detail
99	ROHIT RAGHUWANSHI no accepted messages	12341820	0	200 users	0.0	–	–	1	6h ago	detail
99	SUNIL KUMAR no accepted messages	12342170	0	200 users	0.0	–	–	7	9h ago	detail
99	CHIKATE AJAY DNYANESHWAR incomplete no accepted messages	12340580	0	200 users	0.0	–	–	8	9h ago	detail
99	MALOTH MADHU no accepted messages	12341370	0	200 users	0.0	–	–	1	10h ago	detail
99	SNEHAL SUHANE no accepted messages	12342100	43	200 users	5.0	711	–	7	10h ago	detail
99	MAHARSHI SONI no accepted messages	12341350	0	200 users	0.0	–	–	6	11h ago	detail
99	PANKAJ KASHYAP no accepted messages	P25CS501	0	200 users	0.0	–	–	5	12h ago	detail
99	ASHUTOSH KUMAR JHA no accepted messages	12340390	0	200 users	0.0	–	–	1	14h ago	detail
99	anshu no accepted messages	STARTA	0	200 users	0.0	–	–	3	22h ago	detail
99	VISHLAVATH KARTHIK no accepted messages	12342370	26	200 users	2.5	14	–	6	1d ago	detail
99	SHASHANK YADAV incomplete feed unavailable	12342010	41	200 users	0.7	6637	–	3	1d ago	detail
99	RANGA CHANDRA NAGA VENKATA CHAITANYA KUMAR incomplete feed unavailable	12341740	48	200 users	1.0	1290	–	1	1d ago	detail
99	SIDHESH KUMAR PATRA no accepted messages	12342060	0	200 users	0.0	–	–	3	1d ago	detail
3 · Marks
Mean rank across both boards, and the marks it falls into. Report marks (5) are assessed separately and are not included here.

Name	Roll ID	Static rank	Breakpoint rank	Mean rank	Marks	Runs
OM ANAND	12341510	1	2	1	15	18
YALLAPPAGARI RAHUL DEV REDDY	12342390	2	1	1	15	7
SNEHA NAGMOTI	12342090	4	3	3	15	11
KUNAL SEWAL	12341270	3	5	4	15	5
DONGA SHANMUKHA SIVA VENKATA SAI	12340700	5	6	5	15	1
GALABA VAMSI	12340770	10	4	7	15	17
RAHUL RAJ	12341680	8	7	7	15	7
PARITOSH LAHRE	12341550	6	11	8	12	11
SEERA SHANMUKHA VENKATA RAHUL	12341980	9	9	9	12	6
ABHIGYAN SHARMA	12340050	11	10	10	12	6
ROSHAN RAJ	12341830	12	8	10	12	29
HARSHITA PATIDAR	12340920	13	13	13	12	6
SANNIDHI RITHIKA	12341900	15	12	13	12	1
HIRANNYA MHAISBADWE	12340950	14	14	14	12	3
ADITYA JHA	12340090	16	15	15	12	6
ASHUTOSH KUMAR JHA	12340390	99	99	99	5	1
CHIKATE AJAY DNYANESHWAR	12340580	99	99	99	5	8
GURRALA HANSIKA	12340870	99	99	99	5	3
MAHARSHI SONI	12341350	99	99	99	5	6
MALOTH MADHU	12341370	99	99	99	5	1
RAJEEV KUMAR	12341700	7	99	99	5	12
RANGA CHANDRA NAGA VENKATA CHAITANYA KUMAR	12341740	99	99	99	5	1
ROHIT RAGHUWANSHI	12341820	99	99	99	5	1
SHASHANK YADAV	12342010	99	99	99	5	3
SIDHESH KUMAR PATRA	12342060	99	99	99	5	3
SNEHAL SUHANE	12342100	17	99	99	5	7
SUNIL KUMAR	12342170	99	99	99	5	7
VISHLAVATH KARTHIK	12342370	99	99	99	5	6
PANKAJ KASHYAP	P25CS501	99	99	99	5	5
anshu	STARTA	99	99	99	5	3

