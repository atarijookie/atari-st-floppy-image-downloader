#!/usr/bin/env python3
"""Tests of the file naming in build_list.py:  python3 -I -m unittest discover -s tools"""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from build_list import make_name, names_for_file, slug, stem_of   # noqa: E402


class Naming(unittest.TestCase):
    def test_slug(self):
        self.assertEqual(slug("Bard's Tale, The"), "The-Bards-Tale")
        self.assertEqual(slug("Pang!"), "Pang")
        self.assertEqual(slug("Dungeon Master: Chaos Strikes Back"), "Dungeon-Master-Chaos-Strikes-Back")
        self.assertEqual(slug("Éric's Café & Bar"), "Erics-Cafe-and-Bar")
        self.assertEqual(slug("北京"), "")

    def test_stem(self):
        self.assertEqual(stem_of("disks/AUTO012.MSA"), "AUTO012")
        self.assertEqual(stem_of("A-HA [no number].MSA"), "A-HA-no-number")
        self.assertEqual(stem_of("CON.ST"), "CON_")              # Windows reserved
        self.assertEqual(stem_of("###.st"), "disk")

    def test_name_fits_and_marks_the_rest(self):
        self.assertEqual(make_name("AUTO012", ".msa", ["Arkanoid", "Joust"], 60), "AUTO012_Arkanoid_Joust.msa")
        n = make_name("AUTO012", ".msa", ["Arkanoid", "Joust", "Barbarian", "Dungeon Master", "Xenon 2 Megablast"], 40)
        self.assertEqual(n, "AUTO012_Arkanoid_Joust_Barbarian+.msa")
        self.assertLessEqual(len(n), 40)
        self.assertEqual(make_name("AUTO012", ".msa", [], 60), "AUTO012.msa")

    def test_too_long_first_name_is_cut_at_a_word(self):
        n = make_name("D1", ".st", ["The Very Long Name Of A Game Which Does Not Fit In The Limit At All"], 40)
        self.assertLessEqual(len(n), 40)
        self.assertTrue(n.startswith("D1_The-Very-Long") and n.endswith("+.st"), n)
        self.assertFalse(n[:-4].rstrip("+").endswith("-"), n)

    def test_names_for_file(self):
        content = [{"name": "Joust", "kind": "game", "tag": "[doc]"}, {"name": "Ripper", "kind": "software"},
                   {"name": "Arkanoid", "kind": "game"}]
        self.assertEqual(names_for_file(content), ["Arkanoid"])           # games, not their docs
        self.assertEqual(names_for_file(content[:2]), ["Ripper"])         # no games - the software


if __name__ == "__main__":
    unittest.main()
