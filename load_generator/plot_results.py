"""
plot_results.py — Generate assignment report plots from load generator output.

Usage:
    python3 plot_results.py --results results/results.csv \
                            --utilization results/utilization.csv \
                            --out plots/

Generated plots:
    1.  throughput_overall.png      — overall req/s per experiment
    2.  throughput_write_vs_read.png— write (POST /message) vs read (GET /feed) throughput
    3.  latency_overall.png         — p50/p95/p99 overall per experiment
    4.  latency_write.png           — write-only p50/p95/p99 per experiment
    5.  latency_read.png            — read-only  p50/p95/p99 per experiment
    6.  latency_write_vs_read.png   — side-by-side p50 write vs read
    7.  dropout_comparison.png      — dropout % per experiment
    8.  cpu_utilization.png         — CPU% time-series per system
    9.  valkey_rtt.png              — Valkey RTT time-series per system
    10. endpoint_mix.png            — stacked bar showing write/read request counts
"""

import argparse
import os
import sys

import pandas as pd
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.ticker as mticker
import numpy as np

# ── Style ─────────────────────────────────────────────────────────────────────
plt.rcParams.update({
    "figure.facecolor": "#1e1e2e",
    "axes.facecolor":   "#1e1e2e",
    "axes.edgecolor":   "#6c7086",
    "axes.labelcolor":  "#cdd6f4",
    "xtick.color":      "#cdd6f4",
    "ytick.color":      "#cdd6f4",
    "text.color":       "#cdd6f4",
    "grid.color":       "#313244",
    "grid.linestyle":   "--",
    "legend.facecolor": "#313244",
    "legend.edgecolor": "#6c7086",
    "font.family":      "DejaVu Sans",
    "font.size":        11,
})

WRITE_COLOR  = "#89b4fa"   # blue
READ_COLOR   = "#a6e3a1"   # green
FAIL_COLOR   = "#f38ba8"   # red
PALETTE      = ["#89b4fa", "#a6e3a1", "#fab387", "#cba6f7", "#94e2d5", "#f38ba8"]
P50_COLOR    = "#89b4fa"
P95_COLOR    = "#fab387"
P99_COLOR    = "#f38ba8"


def save(fig, path):
    fig.tight_layout()
    fig.savefig(path, dpi=150, bbox_inches="tight", facecolor=fig.get_facecolor())
    print(f"  ✅ {os.path.basename(path)}")
    plt.close(fig)


def bar_labels(ax, bars, fmt="{:.0f}"):
    for bar in bars:
        h = bar.get_height()
        ax.text(bar.get_x() + bar.get_width() / 2, h + max(h * 0.01, 0.5),
                fmt.format(h), ha="center", va="bottom", fontsize=8)


# ── Results CSV plots ─────────────────────────────────────────────────────────

def plot_results(df: pd.DataFrame, out_dir: str):
    experiments = df["experiment"].tolist()
    x = np.arange(len(experiments))
    colors = [PALETTE[i % len(PALETTE)] for i in range(len(experiments))]

    def setup_bar_ax(ax, title, ylabel):
        ax.set_title(title, fontsize=13, pad=10)
        ax.set_ylabel(ylabel)
        ax.set_xticks(x)
        ax.set_xticklabels(experiments, rotation=20, ha="right")
        ax.grid(axis="y", zorder=0)

    # 1. Overall throughput
    fig, ax = plt.subplots(figsize=(max(8, len(experiments) * 1.6), 5))
    bars = ax.bar(x, df["throughput_rps"], color=colors, width=0.55, zorder=3)
    setup_bar_ax(ax, "Overall Throughput (req/s)", "Throughput (req/s)")
    bar_labels(ax, bars)
    save(fig, os.path.join(out_dir, "throughput_overall.png"))

    # 2. Write vs Read throughput (grouped)
    has_write = "write_throughput_rps" in df.columns and df["write_throughput_rps"].sum() > 0
    has_read  = "read_throughput_rps"  in df.columns and df["read_throughput_rps"].sum()  > 0
    if has_write or has_read:
        fig, ax = plt.subplots(figsize=(max(9, len(experiments) * 2), 5))
        w = 0.35
        offsets = []
        if has_write and has_read:
            offsets = [(-w/2, WRITE_COLOR, "write_throughput_rps", "POST /message"),
                       ( w/2, READ_COLOR,  "read_throughput_rps",  "GET /feed")]
        elif has_write:
            offsets = [(0, WRITE_COLOR, "write_throughput_rps", "POST /message")]
        else:
            offsets = [(0, READ_COLOR, "read_throughput_rps", "GET /feed")]
        for off, col, col_name, label in offsets:
            bars = ax.bar(x + off, df[col_name], width=w, color=col, label=label, zorder=3)
            bar_labels(ax, bars)
        setup_bar_ax(ax, "Write vs Read Throughput (req/s)", "Throughput (req/s)")
        ax.legend()
        save(fig, os.path.join(out_dir, "throughput_write_vs_read.png"))

    # 3. Overall latency p50/p95/p99
    fig, ax = plt.subplots(figsize=(max(9, len(experiments) * 2), 5))
    w = 0.25
    for i, (col, label, col_) in enumerate([
        ("p50_ms", "p50", P50_COLOR),
        ("p95_ms", "p95", P95_COLOR),
        ("p99_ms", "p99", P99_COLOR),
    ]):
        ax.bar(x + (i - 1) * w, df[col], w, label=label, color=col_, zorder=3)
    setup_bar_ax(ax, "Overall Latency Percentiles (ms)", "Latency (ms)")
    ax.legend()
    save(fig, os.path.join(out_dir, "latency_overall.png"))

    # 4. Write-only latency
    if has_write:
        fig, ax = plt.subplots(figsize=(max(9, len(experiments) * 2), 5))
        for i, (col, label, col_) in enumerate([
            ("write_p50_ms", "p50", P50_COLOR),
            ("write_p95_ms", "p95", P95_COLOR),
            ("write_p99_ms", "p99", P99_COLOR),
        ]):
            ax.bar(x + (i - 1) * w, df[col], w, label=label, color=col_, zorder=3)
        setup_bar_ax(ax, "POST /message Latency Percentiles (ms)", "Latency (ms)")
        ax.legend()
        save(fig, os.path.join(out_dir, "latency_write.png"))

    # 5. Read-only latency
    if has_read:
        fig, ax = plt.subplots(figsize=(max(9, len(experiments) * 2), 5))
        for i, (col, label, col_) in enumerate([
            ("read_p50_ms", "p50", P50_COLOR),
            ("read_p95_ms", "p95", P95_COLOR),
            ("read_p99_ms", "p99", P99_COLOR),
        ]):
            ax.bar(x + (i - 1) * w, df[col], w, label=label, color=col_, zorder=3)
        setup_bar_ax(ax, "GET /feed Latency Percentiles (ms)", "Latency (ms)")
        ax.legend()
        save(fig, os.path.join(out_dir, "latency_read.png"))

    # 6. Write p50 vs Read p50 comparison
    if has_write and has_read:
        fig, ax = plt.subplots(figsize=(max(9, len(experiments) * 2), 5))
        w = 0.35
        bars_w = ax.bar(x - w/2, df["write_p50_ms"], w, color=WRITE_COLOR, label="POST /message p50", zorder=3)
        bars_r = ax.bar(x + w/2, df["read_p50_ms"],  w, color=READ_COLOR,  label="GET /feed p50",    zorder=3)
        bar_labels(ax, bars_w)
        bar_labels(ax, bars_r)
        setup_bar_ax(ax, "Write vs Read — Median Latency p50 (ms)", "Latency (ms)")
        ax.legend()
        save(fig, os.path.join(out_dir, "latency_write_vs_read.png"))

    # 7. Dropout
    fig, ax = plt.subplots(figsize=(max(8, len(experiments) * 1.6), 5))
    bars = ax.bar(x, df["dropout_pct"], color=FAIL_COLOR, width=0.55, zorder=3)
    setup_bar_ax(ax, "Dropout Rate (%)", "Dropout (%)")
    for bar, val in zip(bars, df["dropout_pct"]):
        ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 0.01,
                f"{val:.2f}%", ha="center", va="bottom", fontsize=9)
    save(fig, os.path.join(out_dir, "dropout_comparison.png"))

    # 8. Endpoint mix (stacked bar)
    if has_write or has_read:
        fig, ax = plt.subplots(figsize=(max(8, len(experiments) * 1.6), 5))
        if has_write:
            ax.bar(x, df["write_successful"], color=WRITE_COLOR, label="POST /message", zorder=3, width=0.55)
        if has_read:
            bottom = df["write_successful"] if has_write else 0
            ax.bar(x, df["read_successful"],  bottom=bottom, color=READ_COLOR, label="GET /feed", zorder=3, width=0.55)
        setup_bar_ax(ax, "Request Mix per Experiment", "Successful Requests")
        ax.legend()
        save(fig, os.path.join(out_dir, "endpoint_mix.png"))


# ── Utilization CSV plots ─────────────────────────────────────────────────────

def short_label(url: str) -> str:
    port_map = {
        "5270": "Sys2 (Backend-1)",
        "5271": "Sys3 (Backend-2)",
        "5272": "Sys4 (Backend-3)",
        "5269": "Sys1 (LB)",
    }
    for port, label in port_map.items():
        if f":{port}" in url:
            return label
    return url.replace("https://", "").split("/")[0]


def plot_utilization(df: pd.DataFrame, df_res: pd.DataFrame, out_dir: str):
    min_ts = df["timestamp_sec"].min()
    df["t"] = df["timestamp_sec"] - min_ts
    df["label"] = df["url"].apply(short_label)
    systems = df["label"].unique()

    # CPU
    fig, ax = plt.subplots(figsize=(12, 5))
    for i, sys_label in enumerate(systems):
        sub = df[df["label"] == sys_label].sort_values("t")
        ax.plot(sub["t"], sub["cpu_percent"], marker="o", markersize=3,
                label=sys_label, color=PALETTE[i % len(PALETTE)], linewidth=1.5)
    ax.set_title("CPU Utilization Over Time", fontsize=14, pad=12)
    ax.set_xlabel("Time (s into test)")
    ax.set_ylabel("CPU %")
    ax.set_ylim(0, 105)
    ax.legend(loc="upper left")
    ax.grid(zorder=0)
    if df_res is not None and "start_timestamp_sec" in df_res.columns:
        for _, row in df_res.iterrows():
            if row["start_timestamp_sec"] >= min_ts:
                t = row["start_timestamp_sec"] - min_ts
                ax.axvline(t, color="#f38ba8", linestyle=":", alpha=0.5, zorder=1)
                ax.text(t + 0.5, ax.get_ylim()[1]*0.95, row["experiment"], 
                        rotation=90, color="#f38ba8", alpha=0.7, fontsize=8, va="top")

    save(fig, os.path.join(out_dir, "cpu_utilization.png"))

    # Valkey RTT
    fig, ax = plt.subplots(figsize=(12, 5))
    plotted = False
    for i, sys_label in enumerate(systems):
        sub = df[df["label"] == sys_label].sort_values("t")
        if sub["valkey_rtt_ms"].sum() == 0:
            continue
        ax.plot(sub["t"], sub["valkey_rtt_ms"], marker="o", markersize=3,
                label=sys_label, color=PALETTE[i % len(PALETTE)], linewidth=1.5)
        plotted = True
    if plotted:
        ax.set_title("Valkey RTT Over Time (ms)", fontsize=14, pad=12)
        ax.set_xlabel("Time (s into test)")
        ax.set_ylabel("RTT (ms)")
        ax.legend(loc="upper left")
        ax.grid(zorder=0)
        if df_res is not None and "start_timestamp_sec" in df_res.columns:
            for _, row in df_res.iterrows():
                if row["start_timestamp_sec"] >= min_ts:
                    t = row["start_timestamp_sec"] - min_ts
                    ax.axvline(t, color="#f38ba8", linestyle=":", alpha=0.5, zorder=1)
                    ax.text(t + 0.5, ax.get_ylim()[1]*0.95, row["experiment"], 
                            rotation=90, color="#f38ba8", alpha=0.7, fontsize=8, va="top")

        save(fig, os.path.join(out_dir, "valkey_rtt.png"))
    else:
        plt.close(fig)


# ── CLI ───────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Plot load generator results.")
    parser.add_argument("--results",     default="results/results.csv")
    parser.add_argument("--utilization", default="results/utilization.csv")
    parser.add_argument("--out",         default="plots")
    args = parser.parse_args()

    os.makedirs(args.out, exist_ok=True)
    print(f"\n  Generating plots → {args.out}/\n")

    df_res = None
    if os.path.isfile(args.results):
        df_res = pd.read_csv(args.results)
        # Back-fill missing columns for old CSV files
        for col in ["write_successful","write_throughput_rps","write_p50_ms","write_p95_ms","write_p99_ms",
                    "read_successful","read_throughput_rps","read_p50_ms","read_p95_ms","read_p99_ms"]:
            if col not in df_res.columns:
                df_res[col] = 0.0
        if not df_res.empty:
            plot_results(df_res, args.out)
        else:
            print("[WARN] results.csv is empty.")
    else:
        print(f"[WARN] {args.results} not found.")

    if os.path.isfile(args.utilization):
        df = pd.read_csv(args.utilization)
        if not df.empty:
            plot_utilization(df, df_res, args.out)
        else:
            print("[WARN] utilization.csv is empty.")
    else:
        print(f"[INFO] {args.utilization} not found — skipping system utilization plots.")

    print("\n  Done!\n")


if __name__ == "__main__":
    try:
        import matplotlib, pandas, numpy
    except ImportError:
        print("Install dependencies:  pip install matplotlib pandas numpy")
        sys.exit(1)
    main()
