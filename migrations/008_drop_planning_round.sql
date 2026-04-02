-- Drop planning_round from sub_plans — the field was written but never read.
ALTER TABLE sub_plans DROP COLUMN planning_round;
