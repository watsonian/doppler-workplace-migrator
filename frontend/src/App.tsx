import { Routes, Route } from 'react-router-dom';
import WizardLayout from './components/wizard/WizardLayout';
import WelcomePage from './pages/WelcomePage';
import ConfigurationPage from './pages/ConfigurationPage';
import PreflightPage from './pages/PreflightPage';
import GatheringPage from './pages/GatheringPage';
import PlanReviewPage from './pages/PlanReviewPage';
import ManualStepsPage from './pages/ManualStepsPage';
import MigrationPage from './pages/MigrationPage';
import ServiceTokenReportPage from './pages/ServiceTokenReportPage';
import CompletionPage from './pages/CompletionPage';
import ErrorTriagePage from './pages/ErrorTriagePage';
import ScopePage from './pages/ScopePage';
import ComparePage from './pages/ComparePage';
import StandbyPage from './pages/StandbyPage';
import PermissionsPage from './pages/PermissionsPage';
import { InFlightRedirect } from './components/standby/InFlightRedirect';
import CutoverPage from './pages/CutoverPage';

export default function App() {
  return (
    <Routes>
      <Route element={<WizardLayout />}>
        <Route
          path="/"
          element={
            <InFlightRedirect reason="A migration is waiting to cut over, so it opens here. Only one migration can be in progress at a time; a new one can start once this one is cut over.">
              <WelcomePage />
            </InFlightRedirect>
          }
        />
        <Route path="/configure" element={<ConfigurationPage />} />
        <Route path="/preflight" element={<PreflightPage />} />
        <Route path="/gathering" element={<GatheringPage />} />
        <Route
          path="/scope"
          element={
            <InFlightRedirect reason="Scope is chosen once, for the first migration. While it waits to cut over, every top-up covers the whole source, so there is nothing to choose.">
              <ScopePage />
            </InFlightRedirect>
          }
        />
        <Route path="/compare" element={<ComparePage />} />
        <Route path="/plan" element={<PlanReviewPage />} />
        <Route path="/manual-steps" element={<ManualStepsPage />} />
        <Route path="/migrate" element={<MigrationPage />} />
        <Route path="/migrate/errors" element={<ErrorTriagePage />} />
        <Route path="/service-tokens" element={<ServiceTokenReportPage />} />
        <Route path="/cutover" element={<CutoverPage />} />
        <Route path="/standby" element={<StandbyPage />} />
        <Route path="/permissions" element={<PermissionsPage />} />
        <Route path="/complete" element={<CompletionPage />} />
      </Route>
    </Routes>
  );
}
