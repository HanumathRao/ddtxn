from optparse import OptionParser
import commands
import os
from os import system
import socket

parser = OptionParser()
parser.add_option("-s", "--short", action="store_true", dest="short", default=False)
parser.add_option("-p", "--print", action="store_true", dest="dprint", default=False)
parser.add_option("-a", "--allocate", action="store_true", dest="allocate", default=False)
parser.add_option("-n", "--ncores", action="store", type="int", dest="default_ncores", default=8)
parser.add_option("-c", "--contention", action="store", type="int", dest="default_contention", default=100000)
parser.add_option("-l", "--latency", action="store_true", dest="latency", default=False)
parser.add_option("-x", "--rlock", action="store_false", dest="rlock", default=True)
parser.add_option("-d", "--dynamic", action="store_true", dest="dynamic", default=False)
parser.add_option("-k", "--skewed", action="store_true", dest="skewed", default=False)

(options, args) = parser.parse_args()

ben_list_cpus = "socket@0,1,2,7,3-6"

LATENCY_PART = "-latency=%s" % options.latency
DYNAMIC_PART = " -dynamic=%s" % options.dynamic
SKEWED_PART = " -skewed=%s" % options.skewed

BASE_CMD = "GOGC=off numactl -C `list-cpus seq -n %d %s` ./benchmark-rubis -ngo %d -nprocs %d -nsec %d -contention %d -allocate=%s -sys=%d -rlock=%s "+ LATENCY_PART + DYNAMIC_PART + SKEWED_PART

def run_one(fn, cmd):
    if options.dprint:
        print cmd
    status, output = commands.getstatusoutput(cmd)
    if status != 0:
        print "Bad status", status, output
        exit(1)
    if options.dprint:
        print output
    x = output.split()[23].rstrip(',')
    tps = float(x)
    fn.write("%0.2f\t" % tps)

def get_cpus(host):
    ncpus = [2, 4, 10, 20, 30, 40, 50, 60, 70, 80]
    if host == "mat":
        ncpus = [1, 2, 4, 8, 12, 24]
    elif host == "tbilisi":
        ncpus = [1, 2, 4, 8, 12]
    elif host == "tom":
        ncpus = [1, 2, 6, 12, 18, 24, 30, 42, 48]
    if options.short:
        ncpus=[2, 4]
    return ncpus

def fill_cmd(contention, ncpus, systype, cpus_arg=""):
    nsec = 5
    if options.short:
        nsec = 1
    cmd = BASE_CMD % (ncpus, cpus_arg, ncpus, ncpus, nsec, contention, options.allocate, systype, options.rlock)
    return cmd

def scalability_exp(fnpath, host, contention):
    fnn = ""
    if options.skewed:
        fnn = '%s-rubis-scalability-1000000.data' % (host)
    else:
        fnn = '%s-rubis-scalability-%d.data' % (host, contention)
    filename=os.path.join(fnpath, fnn)
    f = open(filename, 'w')
    cpus = get_cpus(host)
    f.write("#\tDoppel\tOCC\n")
    cpu_args = ""
    if host == "ben":
        cpu_args = ben_list_cpus
    for i in cpus:
        f.write("%d"% i)
        f.write("\t")
        cmd = fill_cmd(contention, i, 0, cpu_args)
        run_one(f, cmd)
        f.write("\t")
        cmd = fill_cmd(contention, i, 1, cpu_args)
        run_one(f, cmd)
        f.write("\n")
    f.close()
    system("scp %s tbilisi.csail.mit.edu:/home/neha/src/txn/src/txn/data/" % filename)
    system("scp %s tbilisi.csail.mit.edu:/home/neha/doc/ddtxn-doc/graphs/" % filename)


if __name__ == "__main__":
    host = socket.gethostname()
    if len(host.split(".")) > 1:
        host = host.split(".")[0]
    fnpath = 'tmp/'
    if host == "ben":
        options.default_ncores = 40
    if not os.path.exists(fnpath):
        os.mkdir(fnpath)
    scalability_exp(fnpath, host, options.default_contention)
