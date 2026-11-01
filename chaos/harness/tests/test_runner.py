import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))

from chaos.harness import experiments, run
from chaos.harness.experiments import Context, Result


def test_a_result_passes_only_if_it_checked_something_and_every_check_held():
    r = Result("x", "h")
    assert not r.passed, "an experiment that checks nothing proves nothing"
    r.expect(True, "a")
    assert r.passed
    r.expect(False, "b")
    assert not r.passed


def test_an_experiment_that_crashes_is_a_failure_with_the_traceback_not_a_crashed_runner(tmp_path):
    @experiments.experiment("boom-for-test", "it explodes")
    def boom(ctx, r):
        r.expect(True, "this held")
        raise RuntimeError("the harness itself broke")

    try:
        result = experiments.EXPERIMENTS["boom-for-test"][1](Context(tmp_path))
    finally:
        del experiments.EXPERIMENTS["boom-for-test"]
    assert not result.passed
    assert "the harness itself broke" in result.error
    assert result.seconds >= 0


def test_every_registered_experiment_states_a_hypothesis():
    assert len(experiments.EXPERIMENTS) >= 6
    for name, (hypothesis, fn) in experiments.EXPERIMENTS.items():
        assert hypothesis.strip(), name
        assert callable(fn)


def test_list_names_every_experiment(capsys):
    assert run.main(["--list"]) == 0
    out = capsys.readouterr().out
    for name in experiments.EXPERIMENTS:
        assert name in out


def test_an_unknown_experiment_name_is_an_error_not_a_silent_no_op(capsys):
    assert run.main(["--only", "no-such-experiment"]) == 2
    assert "no-such-experiment" in capsys.readouterr().err


def test_the_report_says_what_failed_and_why():
    ok = Result("fine", "it works")
    ok.expect(True, "held")
    bad = Result("broken", "it should work")
    bad.expect(False, "did not hold")
    bad.findings.append("something measured")
    text = run.render([ok, bad])
    assert "PASS  fine" in text and "FAIL  broken" in text
    assert "FAIL did not hold" in text and "measured: something measured" in text
    assert "1 of 2 experiments passed" in text
    md = run.render_markdown([ok, bad])
    assert "## broken: FAILED" in md and "## fine: passed" in md
