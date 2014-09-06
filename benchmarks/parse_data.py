from optparse import OptionParser
import commands
import os
from os import system
import socket
import subprocess
from plot import Point, Line, Gnuplot

# # 8b5d20e
# # ./buy -nprocs 80 -ngo 80 -nw 80 -nsec 10 -contention 100000 -rr 90 -allocate=False -sys=1 -rlock=False -wr=3.0 -phase=80 -sr=10000 -retry=False -atomic=False -latency=False
# nworkers: 80
# nwmoved: 0
# nrmoved: 0
# sys: 1
# total/sec: 4.0391641537445895e+07
# abortrate: 9.72
# stashrate: 0.00
# rr: 90
# ncrr: 72
# nbids: 1000000
# nproducts: 10
# contention: 100000
# done: 406450603
# actual time: 10.062740397s
# nreads: 387004973
# nbuys: 19445630
# epoch changes: 0
# throughput ns/txn: 24
# naborts: 43750375
# coord time: 0
# coord stats time: 0
# total worker time transitioning: 0
# nstashed: 0
# rlock: false
# wrratio: 3
# nsamples: 0
# getkeys: 0
# ddwrites: 0
# nolock: 7183191
# failv: 0  
# txn0: 19445630
# txn2: 387004973
# chunk-mean: 0
# chunk-stddev: 0

# scale graphs: x-axis is nworkers, y-axis is total/sec.  sys +
# --retry gives different lines.  -rr gives different graphs; 10, 50,
# 90.

# rw graph: nworkers should be 40 on ben.  x-axis is -rr, y-axis is
# total/sec.  sys + --retry is different lines.

# single graph: binary is single, nworkers 40 on ben, x-axis is
# -contention, y-axis is total/sec, -sys+--retry gives different lines

# tom
# 1-18 or 18 cores
# max y-axis of 20M

# ben
# 1-80 or 40 cores
# max y-axis of 82M


parser = OptionParser()
parser.add_option("-g", "--graph", action="store", type="string", dest="graph", default="scale")
parser.add_option("-f", "--file", action="store", type="string", dest="fn", default="buy-data.out")

(options, args) = parser.parse_args()


def wrangle_file(f):
    points = []
    i = 0
    one_point = {}
    for line in f.readlines():
        if line.find("# ./") == 0:
            if i > 0:
                points.append(one_point)
                one_point = {}
            i+=1
            # binary and args
            blah = line.split(" ")
            one_point["binary"] = blah[1][2:].strip()
            #print "binary: ", one_point["binary"]
            for j, pair in enumerate(blah):
                if pair.find("-") is not 0:
                    continue
                else:
                    if pair.find("=") < 0:
                        pair = pair.strip()
                        if pair == "-d" or pair=="-ck" or pair=="-conflicts":
                            continue
                        if j+1 >= len(blah):
                            #print "off the end", blah[j]
                            continue
                        one_point[pair.lstrip("-").strip()] = blah[j+1].strip()
                        continue
                    pairlist = pair.split("=")
                    one_point[pairlist[0][1:]] = pairlist[1]
            continue
        if line.find("# ") == 0:
            continue
        if line.find("BKey") == 0:
            continue
        pair = line.split(": ")
        if len(pair) != 2:
            #print "Pair unsplittable: ", pair
            continue
        name = pair[0].strip()
        val = pair[1].strip()
        one_point[name] = val
    return points
        
def reduce_points(points, xaxis="nw", yaxis="total/sec", *args, **kwargs):
    new_points = []

    # restrict points to ones that match kwargs
    for p in points:
        matches = True
        for name, val in kwargs.items():
            if  not p.has_key(name):
                continue
            if p[name] != val:
                matches = False
                continue
        if matches:
            new_points.append(p)

    # collect appropriate data points, 1 each.  Latest should overwrite oldest.
    graph_points = {}
    for p in new_points:
        xpointval = ""
        try:
            xpointval = int(p[xaxis])
        except:
            print "no", xaxis
            continue
        graph_points[xpointval] = p
    return graph_points

def get_title(x, y):
    if x == "0":
        return "Doppel"
    if x == "1":
        return "OCC"
    if x == "2" and y == "False":
        return "2PL"
    if x == "2" and y == "True":
        return "Atomic"
    raise Exception("Unknown sys:", x, y)


def pp(p):
    print p["binary"], p["sys"], p["contention"], p["nprocs"]

def all_matching(points, *args, **kwargs):
    noval = 0
    if len(points) == 0:
        raise Exception("no points!")
    new_points = []
    for p in points:
        matches = True
        for name, val in kwargs.items():
            if not p.has_key(name):
                noval+=1
                continue
            if p[name] != val:
                matches = False
                #print "did not match", name, "point", p[name], "want", val
                continue
        if matches:
            new_points.append(p)
    return new_points

def output_data(points, yaxis):
    for key in sorted(points):
        print key, "\t", points[key][yaxis]

def make_line(gp, xl, yl, title):
    pp = []
    for i in sorted(gp):
        pp.append(Point(xl, yl, gp[i]))
    line = Line(pp, title)
    return line

def flt(field):
    def fnc(p):
        return float(p[field])
    return fnc

def fltn(field, n):
    def fnc(p):
        return float(p[field])/n
    return fnc

def stat(points, field, fnc=flt("total/sec")):
    sum = 0
    mn = 0
    mx = 0
    if len(points) == 0:
        return None, None, None

    np = points
    if fnc is not None:
        np = map(fnc, points)
    for i,p in enumerate(np):
        if i == 0:
            mn = p
            mx = p
        sum = sum + p
        if p > mx:
            mx = p
        if p < mn:
            mn = p
    return sum/len(points), mn, mx

if __name__ == "__main__":
    f = open('single-data.out.1', 'r')
    points = wrangle_file(f)

    # gp = reduce_points(points, xaxis="rr", yaxis="total/sec", nworkers="20", sys="0", phase="20", binary="buy")
    # line0 = make_line(gp, "rr", "total/sec", "doppel")

    # gp = reduce_points(points, xaxis="rr", yaxis="total/sec", nworkers="20", sys="2", phase="20", binary="buy")
    # line2 = make_line(gp, "rr", "total/sec", "2PL")

    # gp = reduce_points(points, xaxis="rr", yaxis="total/sec", nworkers="20", sys="1", phase="20", binary="buy")
    # line1 = make_line(gp, "rr", "total/sec", "OCC")

    # G = Gnuplot("test", "% read transactions", "Throughput (txns/sec)", [line0, line2, line1])
    # G.eps()

    prob = [0, 1, 5, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100]
    for n in prob:
        one = all_matching(points, nworkers="20", sys="2", binary="single", atomic="True", rr="0", contention=str(n))
        avg, mn, mx = stat(one, "total/sec")
        print n, avg, mn, mx
